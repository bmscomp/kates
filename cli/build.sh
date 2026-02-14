#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
MODULE="github.com/klster/kates-cli/cmd"
OUTPUT_DIR="${OUTPUT_DIR:-dist}"

LDFLAGS="-s -w \
  -X ${MODULE}.Version=${VERSION} \
  -X ${MODULE}.Commit=${COMMIT} \
  -X ${MODULE}.BuildDate=${BUILD_DATE}"

PLATFORMS=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/amd64"
  "linux/arm64"
)

echo "🔨 Building KATES CLI v${VERSION} (${COMMIT})"
echo ""

mkdir -p "${OUTPUT_DIR}"

for platform in "${PLATFORMS[@]}"; do
  os="${platform%/*}"
  arch="${platform#*/}"
  output="${OUTPUT_DIR}/kates-${os}-${arch}"

  echo "  → ${os}/${arch}"
  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build \
    -trimpath \
    -ldflags="${LDFLAGS}" \
    -o "${output}" \
    .

  # Create a compressed tarball
  tar -czf "${output}.tar.gz" -C "${OUTPUT_DIR}" "kates-${os}-${arch}"

  # Generate checksum
  shasum -a 256 "${output}.tar.gz" >> "${OUTPUT_DIR}/checksums.txt"
done

echo ""
echo "✅ Built $(echo "${PLATFORMS[@]}" | wc -w | tr -d ' ') binaries → ${OUTPUT_DIR}/"
echo ""
ls -lh "${OUTPUT_DIR}"/*.tar.gz
echo ""
echo "Checksums:"
cat "${OUTPUT_DIR}/checksums.txt"
