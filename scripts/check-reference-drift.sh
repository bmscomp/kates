#!/usr/bin/env bash
# Guard the book's reference chapters against drift from the implementing
# sources (book-enhancement-plan P1-4). Three checks:
#
#   1. CLI  — every root-level cobra command in cli/cmd/ appears as
#             `kates <cmd>` in the CLI reference chapter(s), and every
#             `kates <cmd>` the chapters mention is a real command or alias.
#   2. gRPC — every service and rpc in kates/src/main/proto/kates.proto
#             appears in the gRPC API chapter.
#   3. REST — every class-level @Path of a JAX-RS resource appears in the
#             REST API chapter (REST clients are excluded).
#
# Static analysis only — no build required, so it runs on every docs PR.
# Usage: scripts/check-reference-drift.sh   (exit 1 on drift; used by docs CI)
set -euo pipefail

cd "$(dirname "$0")/.."

python3 - <<'PY'
import glob, re, sys

fail = False


def err(msg):
    global fail
    print(f"DRIFT: {msg}", file=sys.stderr)
    fail = True


# ── 1. CLI ───────────────────────────────────────────────────────────────
# The CLI reference lives in 10-cli-reference.md today; a split keeps the
# cli- prefix (STYLE.md: slug names, never renumber).
cli_chapters = sorted(
    set(glob.glob('docs/book/10-cli-reference.md') + glob.glob('docs/book/cli-*.md'))
)
cli_doc = '\n'.join(open(f, encoding='utf-8').read() for f in cli_chapters)

src = '\n'.join(open(f, encoding='utf-8').read() for f in glob.glob('cli/cmd/*.go'))
root_vars = set()
for m in re.finditer(r'rootCmd\.AddCommand\(([^)]*)\)', src):
    for v in m.group(1).split(','):
        v = v.strip()
        if v:
            root_vars.add(v)

commands, aliases = set(), set()
for var in root_vars:
    m = re.search(r'\b' + re.escape(var) + r'\s*=\s*&cobra\.Command\{(.*?)\n\}', src, re.S)
    if not m:
        continue  # dynamically registered (e.g. discovered plugins)
    body = m.group(1)
    use = re.search(r'Use:\s*"([^"\s]+)', body)
    if use:
        commands.add(use.group(1))
    al = re.search(r'Aliases:\s*\[\]string\{([^}]*)\}', body)
    if al:
        aliases.update(re.findall(r'"([^"]+)"', al.group(1)))

for cmd in sorted(commands):
    if not re.search(r'\bkates\s+' + re.escape(cmd) + r'\b', cli_doc):
        err(f"CLI command 'kates {cmd}' exists in cli/cmd/ but is undocumented "
            f"in {' + '.join(cli_chapters)}")

known = commands | aliases | {'help', 'completion'}
for f in cli_chapters:
    for i, line in enumerate(open(f, encoding='utf-8'), 1):
        for m in re.finditer(r'\bkates\s+([a-z][a-z0-9_-]+)\b', line):
            if m.group(1) not in known:
                err(f"{f}:{i}: documents 'kates {m.group(1)}' but no such "
                    f"root command or alias exists in cli/cmd/")

# ── 2. gRPC ──────────────────────────────────────────────────────────────
proto = open('kates/src/main/proto/kates.proto', encoding='utf-8').read()
grpc_doc = open('docs/book/16-grpc-api.md', encoding='utf-8').read()
for svc in re.findall(r'^\s*service\s+(\w+)', proto, re.M):
    if svc not in grpc_doc:
        err(f"gRPC service '{svc}' missing from docs/book/16-grpc-api.md")
for rpc in re.findall(r'^\s*rpc\s+(\w+)\(', proto, re.M):
    if rpc not in grpc_doc:
        err(f"gRPC rpc '{rpc}' missing from docs/book/16-grpc-api.md")

# ── 3. REST ──────────────────────────────────────────────────────────────
rest_doc = open('docs/book/11-api-reference.md', encoding='utf-8').read()
for f in glob.glob('kates/src/main/java/**/*.java', recursive=True):
    java = open(f, encoding='utf-8').read()
    if '@RegisterRestClient' in java:
        continue
    m = re.search(r'@Path\("([^"]+)"\)[\s\S]{0,300}?public\s+class', java)
    if m and m.group(1) not in rest_doc:
        err(f"REST path '{m.group(1)}' ({f.split('/')[-1]}) missing from "
            f"docs/book/11-api-reference.md")

sys.exit(1 if fail else 0)
PY

echo "OK: reference chapters match CLI, proto, and REST sources"
