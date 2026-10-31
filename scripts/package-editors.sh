#!/usr/bin/env bash
# Sync, check, and package the Kates editor extensions (VS Code & Visual Studio).
#
# Usage:
#   scripts/package-editors.sh --sync                Copy canonical schemas & grammar into mirror dirs
#   scripts/package-editors.sh --check               Verify schema/grammar parity, manifests & TS compile
#   scripts/package-editors.sh --package [VERSION]   Build dist/vscode-kates-<ver>.vsix & visualstudio-kates-<ver>.zip
set -euo pipefail

cd "$(dirname "$0")/.."

SCHEMAS=(
  "scenario.v1.schema.json"
  "test-request.v1.schema.json"
  "flow.v1.schema.json"
  "resilience.v1.schema.json"
  "disruption.v1.schema.json"
  "kafkareference.v1alpha1.schema.json"
)

MIRROR_SCHEMA_DIRS=(
  "schemas"
  "editors/vscode-kates/schemas"
  "editors/visualstudio-kates/Schemas"
)

sync_assets() {
  for s in "${SCHEMAS[@]}"; do
    for d in "${MIRROR_SCHEMA_DIRS[@]}"; do
      mkdir -p "${d}"
      cp "cli/schemas/${s}" "${d}/${s}"
    done
  done
  cp "editors/vscode-kates/syntaxes/kates-yaml.tmLanguage.json" \
     "editors/visualstudio-kates/Grammars/kates-yaml.tmLanguage.json"
  echo "OK: synced ${#SCHEMAS[@]} JSON schemas and TextMate grammar across editor directories"
}

check_parity() {
  local rc=0
  for s in "${SCHEMAS[@]}"; do
    for d in "${MIRROR_SCHEMA_DIRS[@]}"; do
      if ! cmp -s "cli/schemas/${s}" "${d}/${s}"; then
        echo "ERROR: schema drift: ${d}/${s} differs from cli/schemas/${s} (run 'make editors-sync')" >&2
        rc=1
      fi
    done
  done

  if ! cmp -s "editors/vscode-kates/syntaxes/kates-yaml.tmLanguage.json" \
              "editors/visualstudio-kates/Grammars/kates-yaml.tmLanguage.json"; then
    echo "ERROR: grammar drift between editors/vscode-kates and editors/visualstudio-kates" >&2
    rc=1
  fi

  # Validate JSON manifests, C# project files, and JetBrains/Neovim/Helix configs
  python3 - <<'PY' || rc=1
import json, os, sys, xml.etree.ElementTree as ET

json_files = [
    "editors/vscode-kates/package.json",
    "editors/vscode-kates/package-lock.json",
    "editors/vscode-kates/syntaxes/kates-yaml.tmLanguage.json",
    "editors/vscode-kates/snippets/kates-yaml.json",
    "editors/visualstudio-kates/catalog.json",
    "editors/visualstudio-kates/Grammars/kates-yaml.tmLanguage.json",
]
for f in json_files:
    if not os.path.isfile(f):
        print(f"ERROR: missing editor asset {f}", file=sys.stderr)
        sys.exit(1)
    with open(f, encoding="utf-8") as fh:
        json.load(fh)

required_files = [
    "editors/visualstudio-kates/Kates.VisualStudio.csproj",
    "editors/visualstudio-kates/source.extension.vsixmanifest",
    "editors/visualstudio-kates/KatesLanguageClient.cs",
    "editors/visualstudio-kates/KatesPackage.cs",
    "editors/visualstudio-kates/Commands/RunKatesCommand.cs",
    "editors/visualstudio-kates/pkgdef/kates.pkgdef",
    "editors/jetbrains-kates/jsonSchemas.xml",
    "editors/jetbrains-kates/README.md",
    "editors/neovim-kates/kates_lsp.lua",
    "editors/helix-kates/languages.toml",
]
for f in required_files:
    if not os.path.isfile(f):
        print(f"ERROR: missing editor file {f}", file=sys.stderr)
        sys.exit(1)

tree = ET.parse("editors/jetbrains-kates/jsonSchemas.xml")
for opt in tree.findall(".//option[@name='relativePathToSchema']"):
    rel = opt.attrib.get("value", "").replace("$PROJECT_DIR$/", "")
    if not os.path.isfile(rel):
        print(f"ERROR: JetBrains jsonSchemas.xml references missing schema {rel}", file=sys.stderr)
        sys.exit(1)
PY

  if command -v npm >/dev/null 2>&1; then
    (
      cd editors/vscode-kates
      if [[ ! -d node_modules ]]; then
        npm ci --no-audit --no-fund --silent
      fi
      npm run compile --silent
    )
    echo "OK: VS Code extension TypeScript compiled cleanly"
  fi

  if [[ $rc -ne 0 ]]; then
    exit 1
  fi
  echo "OK: editor schemas, grammars, and manifests verified"
}

package_extensions() {
  local raw_ver="${1:-1.0.0}"
  local ver="${raw_ver#v}"
  # Strip pre-release suffix for SemVer/VSIX manifest compatibility if needed, keep clean version for filenames
  local semver="${ver%%-*}"
  if [[ ! "${semver}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    semver="1.0.0"
  fi

  check_parity

  mkdir -p dist
  local out_dir
  out_dir="$(pwd)/dist"

  # 1. Package VS Code extension (.vsix)
  local tmp_vsc
  tmp_vsc="$(mktemp -d)"
  cp -R editors/vscode-kates/. "${tmp_vsc}/"
  cp LICENSE "${tmp_vsc}/LICENSE"
  python3 - "${tmp_vsc}/package.json" "${tmp_vsc}/package-lock.json" "${semver}" <<'PY'
import json, sys
pkg_path, lock_path, ver = sys.argv[1], sys.argv[2], sys.argv[3]
for path in (pkg_path, lock_path):
    with open(path, encoding="utf-8") as fh:
        data = json.load(fh)
    data["version"] = ver
    if "packages" in data and "" in data["packages"]:
        data["packages"][""]["version"] = ver
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(data, fh, indent=2)
        fh.write("\n")
PY
  (
    cd "${tmp_vsc}"
    if [[ ! -d node_modules ]]; then
      npm ci --no-audit --no-fund --silent
    fi
    npm run compile --silent
    npx --yes @vscode/vsce@3.2.1 package --allow-missing-repository -o "${out_dir}/vscode-kates-${ver}.vsix"
  )
  rm -rf "${tmp_vsc}"
  (cd "${out_dir}" && shasum -a 256 "vscode-kates-${ver}.vsix" > "vscode-kates-${ver}.vsix.sha256")
  echo "OK: packaged dist/vscode-kates-${ver}.vsix"

  # 2. Package Visual Studio 2022 extension source & schema bundle (.zip)
  local tmp_vs
  tmp_vs="$(mktemp -d)"
  mkdir -p "${tmp_vs}/visualstudio-kates"
  cp -R editors/visualstudio-kates/. "${tmp_vs}/visualstudio-kates/"
  cp LICENSE "${tmp_vs}/visualstudio-kates/LICENSE"
  (
    cd "${tmp_vs}"
    zip -rq "${out_dir}/visualstudio-kates-${ver}.zip" visualstudio-kates
  )
  rm -rf "${tmp_vs}"
  (cd "${out_dir}" && shasum -a 256 "visualstudio-kates-${ver}.zip" > "visualstudio-kates-${ver}.zip.sha256")
  echo "OK: packaged dist/visualstudio-kates-${ver}.zip"
}

mode="${1:---check}"
case "${mode}" in
  --sync)
    sync_assets
    ;;
  --check)
    check_parity
    ;;
  --package)
    package_extensions "${2:-1.0.0}"
    ;;
  *)
    echo "Usage: $0 [--sync | --check | --package [VERSION]]" >&2
    exit 2
    ;;
esac
