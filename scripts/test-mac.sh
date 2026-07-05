#!/usr/bin/env bash
# NetLogger macOS test suite — every layer that can run without a second
# mesh node or a human at the GUI. Run from anywhere; exits non-zero on the
# first failing layer.
#
#   Layer 1  static checks     gofmt + go vet (all packages)
#   Layer 2  unit tests        go test ./... (engine + UI + tools; the NIC
#                              parsers, datadir, singleton, iperf resolution,
#                              and ICNS framing are all fixture-tested here)
#   Layer 3  race detector     go test -race on every concurrency-bearing
#                              package (needs cgo — macOS is the only dev
#                              platform in this repo where this runs)
#   Layer 4  cross-GOOS gate   the engine + build-tagged platform files must
#                              compile for windows and linux (catches build-tag
#                              gaps and symbols stranded on another OS)
#   Layer 5  live-glue smoke   nicstat.Collect() against this Mac's real
#                              networksetup/ifconfig/netstat: sorted devices,
#                              closed status vocabulary, speed strings that
#                              are '<n> Gbps/Mbps' or empty — never raw tokens
#   Layer 6  app build         scripts/build-mac.sh, then assert: universal
#                              binary, adhoc signature, Info.plist version ==
#                              internal/version.Version, iconutil accepts the
#                              generated .icns
#
# Phase B items that still need hardware/humans (not covered here): mesh
# join against a Windows node, permission prompts, window chrome, the three
# tests end-to-end, lifecycle checks. See docs/superpowers/plans/
# 2026-07-03-netlogger-macos-port.md Task 11.
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { echo "FAIL: $*" >&2; exit 1; }
say()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

say "Layer 1: gofmt + go vet"
UNFORMATTED="$(gofmt -l cmd internal tools)"
[ -z "${UNFORMATTED}" ] || fail "gofmt: ${UNFORMATTED}"
go vet ./...

say "Layer 2: unit tests (full tree)"
go test ./...

say "Layer 3: race detector"
go test -race ./internal/appcore/ ./internal/discovery/ ./internal/probe/ \
  ./internal/store/ ./internal/iperf/ ./internal/singleton/ ./internal/nicstat/ \
  ./internal/datadir/

say "Layer 4: cross-GOOS compile gate (windows, linux)"
ENGINE_PKGS=(./internal/appcore ./internal/probe ./internal/discovery
  ./internal/iperf ./internal/store ./internal/nicstat ./internal/keepawake
  ./internal/singleton ./internal/firewall ./internal/datadir ./internal/gateway
  ./internal/identity ./internal/applog ./internal/appsettings ./tools/genicon)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build "${ENGINE_PKGS[@]}"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/datadir/ ./internal/nicstat/ ./internal/singleton/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build "${ENGINE_PKGS[@]}"

say "Layer 5: live NIC-glue smoke (real networksetup/ifconfig/netstat)"
SMOKE_DIR="$(mktemp -d)"
trap 'rm -rf "${SMOKE_DIR}"' EXIT
mkdir -p "${SMOKE_DIR}/nicsmoke"
cat > "${SMOKE_DIR}/nicsmoke/main.go" <<'EOF'
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"

	"netlogger/internal/nicstat"
)

// Assertions over live Collect() output: the contracts the UI and the NIC
// change-event detector rely on, checked against this machine's real state.
func main() {
	nics := nicstat.Collect()
	if len(nics) == 0 {
		fmt.Println("FAIL: Collect() returned no NICs on real hardware")
		os.Exit(1)
	}
	speedRe := regexp.MustCompile(`^$|^\d+(\.\d+)? (G|M)bps$`)
	statusOK := map[string]bool{"Up": true, "Disconnected": true, "Unknown": true}
	names := make([]string, 0, len(nics))
	for _, n := range nics {
		names = append(names, n.Name)
		if !speedRe.MatchString(n.LinkSpeed) {
			fmt.Printf("FAIL: %s LinkSpeed %q breaks the '<n> Gbps/Mbps or empty' vocabulary\n", n.Name, n.LinkSpeed)
			os.Exit(1)
		}
		if !statusOK[n.Status] {
			fmt.Printf("FAIL: %s Status %q outside Up/Disconnected/Unknown\n", n.Name, n.Status)
			os.Exit(1)
		}
		if n.RxDiscards != 0 {
			fmt.Printf("FAIL: %s RxDiscards=%d — darwin has no rx-drop source; Drop belongs in TxDiscards\n", n.Name, n.RxDiscards)
			os.Exit(1)
		}
		if n.Description == "" {
			fmt.Printf("FAIL: %s has no human port name from networksetup\n", n.Name)
			os.Exit(1)
		}
	}
	if !sort.StringsAreSorted(names) {
		fmt.Printf("FAIL: devices not sorted: %v\n", names)
		os.Exit(1)
	}
	fmt.Printf("live smoke OK: %d NICs, vocabulary + direction + ordering all conformant\n", len(nics))
}
EOF
# Run from the repo with the file in a module-visible path.
mkdir -p cmd/_nicsmoke
cp "${SMOKE_DIR}/nicsmoke/main.go" cmd/_nicsmoke/main.go
go run ./cmd/_nicsmoke; SMOKE_RC=$?
rm -rf cmd/_nicsmoke
[ "${SMOKE_RC}" -eq 0 ] || exit "${SMOKE_RC}"

say "Layer 6: .app build + bundle assertions"
./scripts/build-mac.sh
# Capture first: `grep -q` under pipefail SIGPIPEs the producer on match.
ARCHS="$(lipo -archs bin/NetLogger.app/Contents/MacOS/netlogger)"
case "${ARCHS}" in *x86_64*arm64*|*arm64*x86_64*) ;; *) fail "not universal: ${ARCHS}";; esac
SIGN_INFO="$(codesign -dv bin/NetLogger.app 2>&1)"
case "${SIGN_INFO}" in *"Signature=adhoc"*) ;; *) fail "missing adhoc signature";; esac
WANT_VERSION="$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/version/version.go)"
GOT_VERSION="$(/usr/libexec/PlistBuddy -c "Print :CFBundleShortVersionString" bin/NetLogger.app/Contents/Info.plist)"
[ "${GOT_VERSION}" = "${WANT_VERSION}" ] || fail "bundle version ${GOT_VERSION} != internal/version.Version ${WANT_VERSION}"
ICONSET="$(mktemp -d)/nl.iconset"
iconutil -c iconset bin/NetLogger.app/Contents/Resources/NetLogger.icns -o "${ICONSET}" || fail "iconutil rejected the generated .icns"
[ "$(ls "${ICONSET}" | wc -l | tr -d ' ')" -ge 8 ] || fail "icns expanded to fewer than 8 entries"

say "All layers green."
