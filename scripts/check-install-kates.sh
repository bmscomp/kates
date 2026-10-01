#!/usr/bin/env bash
# Runs scripts/install-kates.sh against tarballs packed the way a release is.
#
# WHY THIS EXISTS: release.yml packs the CLI as `kates` inside
# kates-<os>-<arch>.tar.gz, while install-kates.sh looked for
# kates-<os>-<arch> inside, so the installer failed against every release
# ever published. Nothing ran it, so nothing noticed. This packs a stand-in
# binary as release.yml's "Package tarball" step does, serves it from a
# file:// URL, and installs it the way `curl ... | bash` would.
#
# Usage:
#   scripts/check-install-kates.sh
set -euo pipefail

cd "$(dirname "$0")/.."
INSTALLER="$PWD/scripts/install-kates.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail=0

check() {
  local name="$1"; shift
  if "$@"; then
    echo "  ok   $name"
  else
    echo "  FAIL $name"
    sed 's/^/       | /' "$work/out"
    fail=1
  fi
}

# run_installer DIR URL [CWD]: runs the installer from CWD, by default an
# empty directory so that it downloads rather than finding a dist/ build.
# Its output lands in $work/out and its exit code in $status.
run_installer() {
  local cwd="${3:-$work/empty}"
  mkdir -p "$1" "$cwd"
  status=0
  (cd "$cwd" && INSTALL_DIR="$1" KATES_DOWNLOAD_URL="$2" bash "$INSTALLER") >"$work/out" 2>&1 || status=$?
}

# The stand-in binary: the installer only copies it and runs `version`.
mkdir -p "$work/stage"
printf '#!/bin/sh\necho "kates stand-in $*"\n' >"$work/stage/kates"
chmod +x "$work/stage/kates"

# Every platform, so whichever one the installer detects is there.
release="$work/release"
mkdir -p "$release"
for os in darwin linux; do
  for arch in amd64 arm64; do
    archive="kates-$os-$arch.tar.gz"
    tar -czf "$release/$archive" -C "$work/stage" kates
    (cd "$release" && shasum -a 256 "$archive") >>"$release/checksums.txt"
  done
done

run_installer "$work/bin" "file://$release"
check "installs a release tarball" test "$status" -eq 0
check "puts the binary in INSTALL_DIR" test -x "$work/bin/kates"
check "runs the binary it installed" grep -q "kates stand-in version" "$work/out"

# A tarball that does not match checksums.txt is refused.
cp -R "$release" "$work/tampered"
sed -E 's/^[0-9a-f]{64}/0000000000000000000000000000000000000000000000000000000000000000/' \
  "$release/checksums.txt" >"$work/tampered/checksums.txt"
run_installer "$work/bin-tampered" "file://$work/tampered"
check "a checksum mismatch fails" test "$status" -ne 0
check "a checksum mismatch installs nothing" test ! -e "$work/bin-tampered/kates"
check "a checksum mismatch says so" grep -q "checksum mismatch" "$work/out"

# No checksums.txt, no install.
cp -R "$release" "$work/unverified"
rm "$work/unverified/checksums.txt"
run_installer "$work/bin-unverified" "file://$work/unverified"
check "a release without checksums.txt fails" test "$status" -ne 0
check "a release without checksums.txt installs nothing" test ! -e "$work/bin-unverified/kates"

# cli/build.sh's tarballs, found in dist/, hold kates-<os>-<arch>.
mkdir -p "$work/checkout/dist"
for os in darwin linux; do
  for arch in amd64 arm64; do
    cp "$work/stage/kates" "$work/stage/kates-$os-$arch"
    tar -czf "$work/checkout/dist/kates-$os-$arch.tar.gz" -C "$work/stage" "kates-$os-$arch"
  done
done
run_installer "$work/bin-local" "file:///nonexistent" "$work/checkout"
check "installs a cli/build.sh tarball from dist/" test -x "$work/bin-local/kates"

# An unsupported platform is named. Its message used to be captured by
# $(detect_platform) and never printed.
mkdir -p "$work/fakebin"
printf '#!/bin/sh\necho Plan9\n' >"$work/fakebin/uname"
chmod +x "$work/fakebin/uname"
PATH="$work/fakebin:$PATH" run_installer "$work/bin-plan9" "file://$release"
check "an unsupported OS fails" test "$status" -ne 0
check "an unsupported OS is named" grep -q "unsupported operating system: Plan9" "$work/out"

exit "$fail"
