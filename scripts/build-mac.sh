#!/usr/bin/env bash
# Release build for macOS: universal binary + NetLogger.app + ad-hoc codesign.
# Requires: macOS 11+, Go 1.26+, Xcode Command Line Tools (xcode-select --install).
# Gio's darwin backend needs cgo, so this CANNOT be cross-compiled from Windows/Linux.
# Keep CFBundleShortVersionString in cmd/netlogger-app/Info.plist in sync with
# internal/version.Version when cutting a release.
set -euo pipefail
cd "$(dirname "$0")/.."

BUILD="$(git rev-parse --short HEAD)$(git diff --quiet || echo '-dirty')"
LDFLAGS="-s -w -X netlogger/internal/version.Build=${BUILD}"

# The Windows COFF resource must not be linked into a darwin binary.
rm -f cmd/netlogger-app/resource.syso

mkdir -p bin
echo "Building darwin/arm64 + darwin/amd64 (build ${BUILD})..."
CGO_ENABLED=1 GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o bin/netlogger-darwin-arm64 ./cmd/netlogger-app
CGO_ENABLED=1 GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o bin/netlogger-darwin-amd64 ./cmd/netlogger-app
lipo -create -output bin/netlogger-universal bin/netlogger-darwin-arm64 bin/netlogger-darwin-amd64

APP="bin/NetLogger.app"
rm -rf "${APP}"
mkdir -p "${APP}/Contents/MacOS" "${APP}/Contents/Resources"
cp cmd/netlogger-app/Info.plist "${APP}/Contents/Info.plist"
cp bin/netlogger-universal "${APP}/Contents/MacOS/netlogger"
go run ./tools/genicon -icns -o "${APP}/Contents/Resources/NetLogger.icns"

# Ad-hoc signature: keeps the firewall "Allow" decision sticky and avoids
# repeated Gatekeeper friction on the local machine.
codesign --force --deep --sign - "${APP}"

echo "Done: ${APP} (build ${BUILD})"
