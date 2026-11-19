#!/usr/bin/env bash
# Cross-compile release binaries into dist/ (used by CI, works locally too).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
rm -rf dist && mkdir dist
build() { # os arch [extra env]
	local name="tuya-homekit-bridge-$1-$2${4:-}"
	env CGO_ENABLED=0 GOOS=$1 GOARCH=$2 ${3:-} go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
		-o "dist/$name" ./cmd/tuya-homekit-bridge
	echo "dist/$name"
}
build linux amd64
build linux arm64
build linux arm GOARM=7 -armv7
build linux arm GOARM=6 -armv6
build linux mips GOMIPS=softfloat -softfloat
build linux mipsle GOMIPS=softfloat -softfloat
build darwin arm64
build darwin amd64
# Compressed variants for small routers (run from RAM, see docs/openwrt.md)
if command -v upx >/dev/null; then
	for f in dist/*-mips-softfloat dist/*-mipsle-softfloat; do
		upx -q --lzma -o "$f.upx" "$f" >/dev/null
	done
fi
(cd dist && sha256sum * > sha256sums.txt 2>/dev/null || shasum -a 256 * > sha256sums.txt)
ls -l dist
