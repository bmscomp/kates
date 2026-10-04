#!/usr/bin/env python3
"""Strict validation of rendered Strimzi custom resources.

Reads a rendered manifest stream on stdin and checks every object whose
apiVersion/kind is served by a Strimzi CRD against that CRD's own
openAPIV3Schema, from the operator chart this repository pins.

Why this exists beside kubeconform: the API server PRUNES a field a structural
CRD does not declare. `helm install` succeeds, the object is stored without the
field, and the feature behind it silently does nothing: the sysctl init
container on a KafkaNodePool, `nodeSelector` on a Connect pod template. A
community schema catalogue may not carry the `v1` Strimzi schemas at all, and
`-ignore-missing-schemas` then turns "not checked" into "passed". This check
reads the CRDs the chart will actually meet and says which field would be
dropped, which required field is missing, and which enum a value falls outside.

It also refuses duplicate mapping keys in ANY document. YAML's "last key wins"
is how a pass-through written next to a chart-owned key (`templateExtra.pod`
beside `pod:`) quietly discards the chart's own settings.

Usage:
  helm template ... | scripts/check-strimzi-crs.py [--crds DIR|TGZ] [--label NAME]

Without --crds the CRDs come from charts/strimzi-operator/charts/
strimzi-kafka-operator-*.tgz (run `helm dependency build charts/strimzi-operator`
first). Exit status: 0 when every object is clean, 1 otherwise, 2 on usage
errors. Findings are printed one per line as `<label> <Kind>/<name>: <finding>`.
"""

import argparse
import glob
import io
import os
import re
import sys
import tarfile

import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


# ── Duplicate-key-aware loading ────────────────────────────────────────────

class _Loader(yaml.SafeLoader):
    pass


def _construct_mapping(loader, node, deep=False):
    seen = {}
    for key_node, _ in node.value:
        key = loader.construct_object(key_node, deep=deep)
        try:
            hash(key)
        except TypeError:
            continue
        if key in seen:
            loader.duplicates.append((key, key_node.start_mark.line + 1))
        seen[key] = True
    return yaml.SafeLoader.construct_mapping(loader, node, deep=deep)


_Loader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, _construct_mapping)


def load_documents(text):
    """Yield (document, [duplicate keys]) for each document in a stream."""
    for chunk in re.split(r"(?m)^---\s*$", text):
        if not chunk.strip():
            continue
        loader = _Loader(chunk)
        loader.duplicates = []
        try:
            doc = loader.get_single_data()
        finally:
            loader.dispose()
        if doc:
            yield doc, loader.duplicates


# ── CRD schemas ────────────────────────────────────────────────────────────

def _crd_texts(source):
    if source is None:
        pattern = os.path.join(ROOT, "charts", "strimzi-operator", "charts", "strimzi-kafka-operator-*.tgz")
        found = sorted(glob.glob(pattern))
        if not found:
            sys.stderr.write("no Strimzi operator chart at %s — run `helm dependency build "
                             "charts/strimzi-operator` or pass --crds\n" % pattern)
            sys.exit(2)
        source = found[-1]
    if os.path.isdir(source):
        for path in sorted(glob.glob(os.path.join(source, "**", "*.yaml"), recursive=True)):
            with open(path) as fh:
                yield fh.read()
        return
    with tarfile.open(source) as tar:
        for member in tar.getmembers():
            if "/crds/" in member.name and member.name.endswith(".yaml"):
                yield tar.extractfile(member).read().decode()


def load_schemas(source):
    schemas = {}
    for text in _crd_texts(source):
        for doc in yaml.safe_load_all(io.StringIO(text)):
            if not doc or doc.get("kind") != "CustomResourceDefinition":
                continue
            spec = doc["spec"]
            for version in spec.get("versions") or []:
                if not version.get("served", True):
                    continue
                schema = (version.get("schema") or {}).get("openAPIV3Schema")
                if schema:
                    key = ("%s/%s" % (spec["group"], version["name"]), spec["names"]["kind"])
                    schemas[key] = schema
    if not schemas:
        sys.stderr.write("no CRD schemas found in %s\n" % source)
        sys.exit(2)
    return schemas


# ── The walk ───────────────────────────────────────────────────────────────

_TYPES = {
    "string": lambda v: isinstance(v, str),
    "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
    "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
    "boolean": lambda v: isinstance(v, bool),
    "object": lambda v: isinstance(v, dict),
    "array": lambda v: isinstance(v, list),
}


def walk(value, schema, path, errors):
    if not schema:
        return
    if value is None:
        if not schema.get("nullable"):
            errors.append("null value at %s" % path)
        return
    if schema.get("x-kubernetes-int-or-string"):
        if not isinstance(value, (int, str)) or isinstance(value, bool):
            errors.append("type %s=%r, expected int-or-string" % (path, value))
        return
    kind = schema.get("type")
    if kind and kind in _TYPES and not _TYPES[kind](value):
        errors.append("type %s=%r, expected %s" % (path, value, kind))
        return
    if "enum" in schema and value not in schema["enum"]:
        errors.append("enum %s=%r not in %s" % (path, value, schema["enum"]))
    if isinstance(value, str) and schema.get("pattern") and not re.search(schema["pattern"], value):
        errors.append("pattern %s=%r does not match %s" % (path, value, schema["pattern"]))
    if isinstance(value, dict):
        if schema.get("x-kubernetes-preserve-unknown-fields"):
            return
        props = schema.get("properties")
        extra = schema.get("additionalProperties")
        for key in schema.get("required") or []:
            if key not in value:
                errors.append("missing required %s.%s" % (path, key))
        for key, sub in value.items():
            if props is not None and key in props:
                walk(sub, props[key], "%s.%s" % (path, key), errors)
            elif isinstance(extra, dict):
                walk(sub, extra, "%s.%s" % (path, key), errors)
            elif props is not None and extra is not True:
                errors.append("unknown field %s.%s (the API server would prune it)" % (path, key))
    elif isinstance(value, list):
        for i, item in enumerate(value):
            walk(item, schema.get("items"), "%s[%d]" % (path, i), errors)


def check(stream, schemas, label):
    findings, checked = [], 0
    for doc, duplicates in load_documents(stream):
        name = "%s/%s" % (doc.get("kind", "?"), (doc.get("metadata") or {}).get("name", "?"))
        for key, line in duplicates:
            findings.append("%s %s: duplicate key %r (line %d of the document) — the earlier value is discarded"
                            % (label, name, key, line))
        schema = schemas.get((doc.get("apiVersion"), doc.get("kind")))
        if schema is None:
            continue
        checked += 1
        errors = []
        spec_schema = (schema.get("properties") or {}).get("spec")
        if "spec" in doc:
            walk(doc["spec"], spec_schema, ".spec", errors)
        elif spec_schema and "spec" in (schema.get("required") or []):
            errors.append("missing required .spec")
        findings.extend("%s %s: %s" % (label, name, e) for e in errors)
    return findings, checked


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--crds", help="directory of CRD YAML files, or an operator chart .tgz")
    ap.add_argument("--label", default="render")
    ap.add_argument("--quiet", action="store_true")
    args = ap.parse_args()
    schemas = load_schemas(args.crds)
    findings, checked = check(sys.stdin.read(), schemas, args.label)
    for f in findings:
        print(f)
    if not args.quiet and not findings:
        print("OK: %s — %d Strimzi resources match the pinned CRD schemas" % (args.label, checked))
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main())
