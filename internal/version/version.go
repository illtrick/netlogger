// Package version holds the build version string for NetLogger.
package version

// Version is the NetLogger release identifier.
const Version = "1.0.0"

// Build identifies the exact binary, stamped at link time from the git short
// hash (see scripts/build-app.ps1: -X netlogger/internal/version.Build=...).
// It stays "dev" for un-stamped (e.g. `go run`) builds. Used to detect when the
// mesh is running mismatched binaries, which silently breaks cross-machine
// features like synchronized reset.
var Build = "dev"
