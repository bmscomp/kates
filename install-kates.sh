#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="kates"
REPO_BASE_URL="${KATES_DOWNLOAD_URL:-https://github.com/klster/kates-cli/releases/latest/download}"

detect_platform() {
  local os arch

  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux)  os="linux"  ;;
    *)
      echo "Error: unsupported operating system: $(uname -s)"
      echo "KATES CLI supports macOS and Linux only."
      exit 1
      ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64)   arch="amd64" ;;
    arm64|aarch64)   arch="arm64" ;;
    *)
      echo "Error: unsupported architecture: $(uname -m)"
      echo "KATES CLI supports amd64 and arm64 only."
      exit 1
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

  echo "  Platform:  ${os}/${arch}"
  echo "  Install:   ${INSTALL_DIR}/${BINARY}"
  echo ""

  # Check if we have a local dist/ directory (dev install)
  local local_binary="dist/kates-${os}-${arch}"
  if [ -f "${local_binary}" ]; then
    echo "  → Found local build: ${local_binary}"
    install_from_file "${local_binary}"
    return
  fi

  # Check for local tarball
  local local_tarball="dist/kates-${os}-${arch}.tar.gz"
  if [ -f "${local_tarball}" ]; then
    echo "  → Found local tarball: ${local_tarball}"
    install_from_tarball "${local_tarball}" "${os}" "${arch}"
    return
  fi

  # Download from remote
  local url="${REPO_BASE_URL}/kates-${os}-${arch}.tar.gz"
  echo "  → Downloading from: ${url}"

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "${tmpdir}"' EXIT

  local tarball="${tmpdir}/kates.tar.gz"

  if command -v curl &>/dev/null; then
    curl -fsSL -o "${tarball}" "${url}"
  elif command -v wget &>/dev/null; then
    wget -q -O "${tarball}" "${url}"
  else
    echo "Error: curl or wget is required"
    exit 1
  fi

  install_from_tarball "${tarball}" "${os}" "${arch}"
}

install_from_tarball() {
  local tarball="$1" os="$2" arch="$3"
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "${tmpdir}"' EXIT

  tar -xzf "${tarball}" -C "${tmpdir}"

  local extracted="${tmpdir}/kates-${os}-${arch}"
  if [ ! -f "${extracted}" ]; then
    echo "Error: expected binary not found in archive"
    exit 1
  fi

  install_from_file "${extracted}"
}

install_from_file() {
  local src="$1"

  chmod +x "${src}"

  if [ -w "${INSTALL_DIR}" ]; then
    cp "${src}" "${INSTALL_DIR}/${BINARY}"
  else
    echo "  → Requires sudo to install to ${INSTALL_DIR}"
    sudo cp "${src}" "${INSTALL_DIR}/${BINARY}"
    sudo chmod +x "${INSTALL_DIR}/${BINARY}"
  fi

  echo ""
  echo "  ✓ Installed: $(command -v ${BINARY} || echo "${INSTALL_DIR}/${BINARY}")"
  echo ""

  # Verify
  if command -v "${BINARY}" &>/dev/null; then
    echo "  Version info:"
    "${BINARY}" version 2>/dev/null || true
  fi

  echo ""
  echo "  Get started:"
  echo "    kates config set-context local --url http://localhost:30083"
  echo "    kates health"
  echo "    kates status"
  echo ""
}

main "$@"
