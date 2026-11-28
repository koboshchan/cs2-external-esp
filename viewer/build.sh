#!/usr/bin/env sh
# Builds the viewer for macOS (arm64) and Windows (amd64).
#
# macOS: native AppKit launcher and transparent click-through overlay.
# Windows: layered click-through overlay, pure Go (no cgo).
set -eu

cd "$(dirname "$0")"
mkdir -p dist

echo "== tests =="
go test ./...

echo "== darwin/arm64 =="
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/cs2-remote-viewer-macos-arm64 .

echo "== windows/amd64 =="
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/cs2-remote-viewer.exe .

ls -la dist
