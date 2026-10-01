#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="kates"
REPO_BASE_URL="${KATES_DOWNLOAD_URL:-https://github.com/bmscomp/kates/releases/latest/download}"

# One scratch directory for the whole run. install_from_tarball used to set
# its own EXIT trap, which replaced main's, so the download was never removed.
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

# Errors go to stderr: detect_platform runs inside $(...), which captured
# them, so an unsupported platform exited 1 after printing nothing.
die() {
  echo "Error: $*" >&2
  exit 1
}

detect_platform() {
  local os arch

  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux)  os="linux"  ;;
    *)
      die "unsupported operating system: $(uname -s)
KATES CLI supports macOS and Linux only."
      ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64)   arch="amd64" ;;
    arm64|aarch64)   arch="arm64" ;;
    *)
      die "unsupported architecture: $(uname -m)
KATES CLI supports amd64 and arm64 only."
      ;;
  esac

  echo "${os}/${arch}"
}

main() {
  echo ""
  echo "  ╭──────────────────────────────────────╮"
  echo "  │   KATES CLI Installer                │"
  echo "  │   Kafka Advanced Testing Suite       │"
  echo "  ╰──────────────────────────────────────╯"
  echo ""

  local platform
  platform="$(detect_platform)"
  local os="${platform%/*}"
  local arch="${platform#*/}"
  local name="kates-${os}-${arch}"

  echo "  Platform:  ${os}/${arch}"
  echo "  Install:   ${INSTALL_DIR}/${BINARY}"
  echo ""

  # Check if we have a local dist/ directory (dev install)
  local local_binary="dist/${name}"
  if [ -f "${local_binary}" ]; then
    echo "  → Found local build: ${local_binary}"
    install_from_file "${local_binary}"
    return
  fi

  # Check for local tarball
  local local_tarball="dist/${name}.tar.gz"
  if [ -f "${local_tarball}" ]; then
    echo "  → Found local tarball: ${local_tarball}"
    install_from_tarball "${local_tarball}"
    return
  fi

  # Download from remote
  local url="${REPO_BASE_URL}/${name}.tar.gz"
  echo "  → Downloading from: ${url}"

  local tarball="${WORK_DIR}/${name}.tar.gz"
  local sums="${WORK_DIR}/checksums.txt"
  fetch "${url}" "${tarball}" || die "could not download ${url}"
  fetch "${REPO_BASE_URL}/checksums.txt" "${sums}" \
    || die "could not download ${REPO_BASE_URL}/checksums.txt, so the download cannot be verified"
  verify_checksum "${tarball}" "${name}.tar.gz" "${sums}"

  install_from_tarball "${tarball}"
}

fetch() {
  local url="$1" dest="$2"
  if command -v curl &>/dev/null; then
    curl -fsSL -o "${dest}" "${url}"
  elif command -v wget &>/dev/null; then
    wget -q -O "${dest}" "${url}"
  else
    die "curl or wget is required"
  fi
}

sha256_of() {
  if command -v sha256sum &>/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum &>/dev/null; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "sha256sum or shasum is required to verify the download"
  fi
}

# verify_checksum FILE NAME SUMS: fails unless SUMS, the release's
# checksums.txt, gives NAME the SHA-256 of FILE.
verify_checksum() {
  local file="$1" name="$2" sums="$3"
  local expected actual
  # A line is "<sha256>  <file>"; the file may carry a directory or the
  # binary-mode "*", so the last path element is what is compared.
  expected="$(awk -v want="${name}" '{ f = $2; sub(/^\*/, "", f); sub(/.*\//, "", f); if (f == want) { print $1; exit } }' "${sums}")"
  [ -n "${expected}" ] || die "checksums.txt has no entry for ${name}"
  actual="$(sha256_of "${file}")"
  if [ "${actual}" != "${expected}" ]; then
    die "checksum mismatch for ${name}
  expected ${expected}
  got      ${actual}"
  fi
  echo "  ✓ Checksum verified"
}

install_from_tarball() {
  local tarball="$1"
  local dir="${WORK_DIR}/extract"
  mkdir -p "${dir}"

  tar -xzf "${tarball}" -C "${dir}"

  # Release tarballs hold the binary as `kates`; cli/build.sh's hold it as
  # kates-<os>-<arch>. Only the second was looked for, so installing from a
  # release always failed.
  local candidate
  for candidate in "${dir}/${BINARY}" "${dir}/$(basename "${tarball}" .tar.gz)"; do
    if [ -f "${candidate}" ]; then
      install_from_file "${candidate}"
      return
    fi
  done
  die "no ${BINARY} binary in $(basename "${tarball}")"
}

install_from_file() {
  local src="$1"
  local dest="${INSTALL_DIR}/${BINARY}"

  chmod +x "${src}"

  if [ -w "${INSTALL_DIR}" ]; then
    cp "${src}" "${dest}"
  else
    echo "  → Requires sudo to install to ${INSTALL_DIR}"
    sudo cp "${src}" "${dest}"
    sudo chmod +x "${dest}"
  fi

  echo ""
  echo "  ✓ Installed: ${dest}"
  echo ""

  # Run the binary just installed. `command -v kates` can find another one
  # earlier on PATH, a Homebrew install say, whose version proves nothing.
  echo "  Version info:"
  "${dest}" version 2>/dev/null || echo "  Warning: ${dest} did not run" >&2

  local on_path
  on_path="$(command -v "${BINARY}" || true)"
  if [ -z "${on_path}" ]; then
    echo ""
    echo "  Note: ${INSTALL_DIR} is not on your PATH."
  elif [ ! "${on_path}" -ef "${dest}" ]; then
    echo ""
    echo "  Note: \`${BINARY}\` on your PATH is ${on_path}, not the one just installed."
  fi

  echo ""
  echo "  Get started:"
  echo "    kates ctx set local --url http://localhost:30083"
  echo "    kates ctx use local"
  echo "    kates health"
  echo ""
}

main "$@"
