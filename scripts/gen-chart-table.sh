#!/usr/bin/env bash
# Keep the README Helm chart table in sync with charts/*/Chart.yaml.
#
# Usage:
#   scripts/gen-chart-table.sh           Print a scaffold table generated from
#                                        Chart.yaml metadata (descriptions come
#                                        from each chart's description field).
#   scripts/gen-chart-table.sh --check   Verify the table between the
#                                        <!-- chart-table:start/end --> markers
#                                        in README.md: every chart has a row,
#                                        no orphan rows, and the Version /
#                                        App Version columns match Chart.yaml.
#                                        Exits non-zero on drift (used by
#                                        `make readme-check` and docs CI).
#
# Hand-written row descriptions in README.md are preserved: --check only
# validates chart presence and version columns, keyed on the row's
# ](charts/<dir>/) link.
set -euo pipefail

cd "$(dirname "$0")/.."
README=README.md

chart_field() { # <chart-dir> <yaml-key>
  grep -E "^$2:" "charts/$1/Chart.yaml" | head -1 \
    | sed -E "s/^$2:[[:space:]]*//; s/^[\"']//; s/[\"']$//"
}

print_table() {
  echo "| Chart | Version | App Version | Description |"
  echo "|:------|:--------|:------------|:------------|"
  local dir name
  for dir in charts/*/; do
    name=$(basename "$dir")
    printf '| [`%s`](charts/%s/) | %s | %s | %s |\n' \
      "$name" "$name" \
      "$(chart_field "$name" version)" \
      "$(chart_field "$name" appVersion)" \
      "$(chart_field "$name" description)"
  done
}

check_table() {
  local fail=0 region dir name row v av row_v row_av link
  region=$(sed -n '/<!-- chart-table:start -->/,/<!-- chart-table:end -->/p' "$README")
  if [[ -z "$region" ]]; then
    echo "ERROR: chart-table markers not found in $README" >&2
    return 1
  fi

  for dir in charts/*/; do
    name=$(basename "$dir")
    row=$(grep -F "](charts/$name/)" <<<"$region" || true)
    if [[ -z "$row" ]]; then
      echo "MISSING: no row for chart '$name' in the $README chart table" >&2
      fail=1
      continue
    fi
    v=$(chart_field "$name" version)
    av=$(chart_field "$name" appVersion)
    row_v=$(awk -F'|' '{gsub(/^[ \t]+|[ \t]+$/, "", $3); print $3}' <<<"$row")
    row_av=$(awk -F'|' '{gsub(/^[ \t]+|[ \t]+$/, "", $4); print $4}' <<<"$row")
    if [[ "$row_v" != "$v" ]]; then
      echo "STALE: chart '$name' version is '$v' but $README says '$row_v'" >&2
      fail=1
    fi
    if [[ "$row_av" != "$av" ]]; then
      echo "STALE: chart '$name' appVersion is '$av' but $README says '$row_av'" >&2
      fail=1
    fi
  done

  while IFS= read -r link; do
    if [[ ! -d "charts/$link" ]]; then
      echo "ORPHAN: $README chart table references missing chart '$link'" >&2
      fail=1
    fi
  done < <(grep -oE '\]\(charts/[A-Za-z0-9_-]+/\)' <<<"$region" \
             | sed -E 's/^\]\(charts\///; s/\/\)$//' | sort -u)

  if [[ $fail -ne 0 ]]; then
    echo "" >&2
    echo "Chart table drift detected. Run scripts/gen-chart-table.sh for a fresh scaffold," >&2
    echo "then update the table between the chart-table markers in $README." >&2
    return 1
  fi
  echo "OK: $README chart table is in sync with charts/*/Chart.yaml"
}

case "${1:-}" in
  --check) check_table ;;
  "")      print_table ;;
  *)       echo "Usage: $0 [--check]" >&2; exit 2 ;;
esac
