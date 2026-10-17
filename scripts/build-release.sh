#!/bin/sh
set -eu
version="${1:-dev}"
cd "$(dirname "$0")/.."
rm -rf dist
for target in windows/amd64 darwin/arm64 darwin/amd64 linux/amd64; do
	os="${target%/*}"
	arch="${target#*/}"
	out="dist/booruscope-$version-$os-$arch"
	mkdir -p "$out"
	ext=""
	flags="-s -w"
	if [ "$os" = windows ]; then
		ext=".exe"
		flags="$flags -H=windowsgui"
	fi
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$flags" -o "$out/booruscope$ext" ./cmd/booruscope
	cp "$out/booruscope$ext" "$out/BooruScope-Setup$ext"
	cp LICENSE README.md "$out/"
	echo "built $out"
done
