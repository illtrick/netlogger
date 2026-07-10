// Package version holds the build identity for NetLogger.
//
// Three orthogonal facts describe a running binary:
//   - Version  — the semantic release (e.g. "1.1.0"). This is the COMPATIBILITY
//     contract: two nodes on the same Version speak the same wire protocol and
//     interoperate, regardless of OS or CPU. Bump it when the protocol changes.
//   - Platform — GOOS/GOARCH (e.g. "windows/amd64", "darwin/arm64"). Two nodes
//     on the same Version but different Platforms are EXPECTED to have different
//     binaries; that is not a problem.
//   - Build    — the exact git commit the binary was built from. A fine-grained
//     identity for spotting an incomplete same-OS rollout; it is deliberately
//     NOT the compatibility signal (the same commit yields a different Build-less
//     truth per platform, and re-tagged releases must still interoperate).
package version

import "runtime"

// Version is the NetLogger release identifier — the mesh compatibility contract.
const Version = "1.3.5"

// Build identifies the exact binary, stamped at link time from the git short
// hash (see scripts/build-app.ps1: -X netlogger/internal/version.Build=...).
// It stays "dev" for un-stamped (e.g. `go run`) builds.
var Build = "dev"

// Platform reports the OS/CPU this binary targets, e.g. "windows/amd64" or
// "darwin/arm64". Used so the mesh can tell an expected cross-platform binary
// difference apart from an actual version/rollout mismatch.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
