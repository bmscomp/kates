#!/usr/bin/env bash
# convert-callouts.sh — Convert GitHub alert callouts to Quarto callout syntax
#
# Usage: ./scripts/convert-callouts.sh [--dry-run] [file ...]
#        Without arguments, processes all .md files in docs/book/
#
# Converts:
#   > [!NOTE]             →  ::: {.callout-note}
#   > Content here        →  Content here
#   (blank line)          →  :::
#
# Supports: NOTE, TIP, IMPORTANT, WARNING, CAUTION
# Compatible with macOS and GNU systems.

set -euo pipefail

DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
  shift
fi

# Determine files to process
if [[ $# -gt 0 ]]; then
  FILES=("$@")
else
  FILES=(docs/book/*.md)
fi

TOTAL_CONVERTED=0

convert_file() {
  local file="$1"
  local tmpfile="${file}.tmp"
  local in_callout=false

  while IFS= read -r line || [[ -n "$line" ]]; do
    # Check for GitHub alert start: > [!TYPE]
    if [[ "$line" =~ ^\>\ \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\] ]]; then
      local type="${BASH_REMATCH[1]}"
      local type_lower
      type_lower=$(echo "$type" | tr '[:upper:]' '[:lower:]')
      echo "::: {.callout-${type_lower}}"
      in_callout=true
      continue
    fi

    if [[ "$in_callout" == true ]]; then
      # Lines starting with "> " — strip the blockquote prefix
      if [[ "$line" =~ ^\>\ (.*) ]]; then
        echo "${BASH_REMATCH[1]}"
        continue
      fi
      # Empty blockquote line
      if [[ "$line" == ">" ]]; then
        echo ""
        continue
      fi
      # End of callout — first line not starting with ">"
      echo ":::"
      echo ""
      in_callout=false
      echo "$line"
      continue
    fi

    # Regular line
    echo "$line"
  done < "$file" > "$tmpfile"

  # Close callout if file ends inside one
  if [[ "$in_callout" == true ]]; then
    echo ":::" >> "$tmpfile"
  fi

  mv "$tmpfile" "$file"
}

for file in "${FILES[@]}"; do
  if [[ ! -f "$file" ]]; then
    echo "  ⚠ Skipping: $file (not found)"
    continue
  fi

  # Count callouts in this file
  count=$(grep -cE '> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]' "$file" 2>/dev/null || true)
  if [[ "$count" -eq 0 ]]; then
    continue
  fi

  echo "  📝 $file — $count callout(s)"
  TOTAL_CONVERTED=$((TOTAL_CONVERTED + count))

  if [[ "$DRY_RUN" == true ]]; then
    continue
  fi

  convert_file "$file"
done

echo ""
if [[ "$DRY_RUN" == true ]]; then
  echo "  🔍 Dry run: $TOTAL_CONVERTED callout(s) would be converted"
else
  echo "  ✅ Converted $TOTAL_CONVERTED callout(s)"
fi
