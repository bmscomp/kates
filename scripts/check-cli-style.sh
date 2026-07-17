#!/usr/bin/env bash
# Style gate for the kates CLI: every color comes from cli/pkg/theme.
#
# WHY THIS EXISTS: the CLI's colors were once scattered across dozens of
# hardcoded `lipgloss.Color("#…")` literals, so "green" meant five different
# greens and the blue=OK / red=KO convention was applied file-by-file, or not
# at all. The hex sweep moved every literal onto the semantic tokens in
# cli/pkg/theme. This script is the assertion that keeps it that way: no new
# hex literals, no raw ANSI escapes that bypass lipgloss's terminal handling.
#
# Usage:
#   scripts/check-cli-style.sh          Run every check against tracked files.
set -euo pipefail

cd "$(dirname "$0")/.."

fail=0

# ── No hardcoded hex colors ─────────────────────────────────────────────────
# cli/pkg/theme is where tokens are defined; cli/cmd/theme.go's hex palette is
# DATA (user-selectable theme definitions), not styling. Both are excluded —
# everything else must use theme tokens.
hex_offenders="$(git grep -n 'lipgloss\.Color("#' -- ':(glob)cli/**/*.go' \
  ':(exclude)cli/pkg/theme' \
  ':(exclude)cli/cmd/theme.go' \
  || true)"

if [[ -n "$hex_offenders" ]]; then
  echo "  FAIL hardcoded lipgloss hex colors (use cli/pkg/theme tokens instead):"
  echo "$hex_offenders" | sed 's/^/       /'
  fail=1
else
  echo "  ok   no hardcoded lipgloss hex colors outside the theme"
fi

# ── No raw ANSI escape literals ─────────────────────────────────────────────
# Raw \033[ / \x1b[ literals bypass lipgloss's capability detection (NO_COLOR,
# TERM=dumb, piped output) — exactly the bugs check-cli-compat.sh exists to
# catch. cli/output/ is the rendering layer and the only package allowed to
# speak raw escapes (glyph fallbacks, the ClearFrame screen control).
#
# _test.go files are excluded: tests for escape-stripping necessarily embed
# escapes as fixture DATA — they are inputs to assertions, not styling.
#
# cli/cmd/deploy_view.go is grandfathered: its remaining raw escapes are
# removed by the wave-3 deploy summary rewrite, which may land in this same
# PR. Once `grep -c 'x1b\[' cli/cmd/deploy_view.go` reports 0, delete the
# exclude line below.
esc_offenders="$(git grep -nE '\\033\[|\\x1b\[' -- ':(glob)cli/**/*.go' \
  ':(exclude)cli/output/' \
  ':(exclude)cli/**/*_test.go' \
  || true)"

if [[ -n "$esc_offenders" ]]; then
  echo "  FAIL raw ANSI escape literals (route through cli/output or lipgloss):"
  echo "$esc_offenders" | sed 's/^/       /'
  fail=1
else
  echo "  ok   no raw ANSI escape literals outside cli/output/"
fi

if [[ "$fail" -ne 0 ]]; then
  echo
  echo "CLI style violations found." >&2
  exit 1
fi
echo "OK: CLI style checks pass"
