#!/usr/bin/env bash
# Terminal-compatibility gate for the kates CLI.
#
# WHY THIS EXISTS: the CLI once forced TrueColor on every run, writing raw
# 38;2 escape sequences into pipes and CI logs and silently ignoring NO_COLOR.
# Nothing caught it, because nothing asserted on the raw bytes the binary
# emits. This script is that assertion. Every check here runs the REAL binary
# under the exact conditions CI and scripts use it in.
#
# Usage:
#   scripts/check-cli-compat.sh          Build the CLI and run every check.
set -euo pipefail

cd "$(dirname "$0")/.."

BIN="$(mktemp -d)/kates"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "Building CLI..."
(cd cli && go build -o "$BIN" .)

fail=0

# has_escapes FILE — true when the file contains any ESC byte.
has_escapes() { LC_ALL=C grep -q $'\x1b' "$1"; }
# has_nonascii FILE — true when the file contains bytes outside printable ASCII
# plus whitespace.
has_nonascii() { LC_ALL=C grep -q '[^ -~\t\r]' "$1"; }

check() {
  local name="$1"; shift
  if "$@"; then
    echo "  ok   $name"
  else
    echo "  FAIL $name"
    fail=1
  fi
}

out="$(mktemp)"

# ── Piped output carries no escape codes ────────────────────────────────────
# stdout is a pipe here, exactly like `kates ... | tee` or a CI log.
"$BIN" version > "$out" 2>&1
check "piped 'version' is escape-free" bash -c "! LC_ALL=C grep -q \$'\x1b' '$out'"

"$BIN" --help > "$out" 2>&1
check "piped '--help' is escape-free" bash -c "! LC_ALL=C grep -q \$'\x1b' '$out'"

# ── NO_COLOR and TERM=dumb are honored ──────────────────────────────────────
NO_COLOR=1 "$BIN" version > "$out" 2>&1
check "NO_COLOR is honored" bash -c "! LC_ALL=C grep -q \$'\x1b' '$out'"

TERM=dumb "$BIN" version > "$out" 2>&1
check "TERM=dumb is honored" bash -c "! LC_ALL=C grep -q \$'\x1b' '$out'"

# ── --plain and KATES_ASCII produce pure ASCII ──────────────────────────────
# Plain mode is a statement that the most limited consumer is reading: no
# escapes AND no multibyte glyphs.
"$BIN" --plain version > "$out" 2>&1
check "--plain has no escapes"   bash -c "! LC_ALL=C grep -q \$'\x1b' '$out'"
check "--plain is pure ASCII"    bash -c "! LC_ALL=C grep -q '[^ -~\t\r]' '$out'"

KATES_ASCII=1 "$BIN" version > "$out" 2>&1
check "KATES_ASCII glyphs are ASCII" bash -c "! LC_ALL=C grep -q '[^ -~\t\r]' '$out'"

# ── Unset locale keeps UTF-8 glyphs (the polarity rule) ─────────────────────
# Containers and CI run with no locale set and render UTF-8 fine; stripping
# glyphs there would degrade exactly where most output is read. This asserts
# the ASCII fallback does NOT engage on absence of evidence.
env -u LANG -u LC_ALL -u LC_CTYPE "$BIN" version > "$out" 2>&1
check "unset locale keeps UTF-8 glyphs" bash -c "LC_ALL=C grep -q '[^ -~\t\r]' '$out'"

# ── TUIs refuse politely without a terminal ─────────────────────────────────
# These must exit non-zero fast with a readable message — not hang, not crash
# with a raw bubbletea error, not dump alt-screen sequences.
set +e
"$BIN" lab < /dev/null > "$out" 2>&1
rc=$?
set -e
check "'lab' without TTY exits non-zero" test "$rc" -ne 0
check "'lab' refusal mentions the terminal" grep -qi "terminal" "$out"
check "'lab' refusal is escape-free... " bash -c "! LC_ALL=C grep -q \$'\x1b' '$out'"

rm -f "$out"

if [[ "$fail" -ne 0 ]]; then
  echo
  echo "Terminal-compatibility regressions found." >&2
  exit 1
fi
echo "OK: CLI terminal-compatibility checks pass"
