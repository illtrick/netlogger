# Release build for NetLogger.
#
#   - bin/netlogger.exe        Windows GUI app (no console window — double-click)
#   - bin/netlogger-cli.exe    Windows console build (verbose CLI/service mgmt)
#   - bin/netlogger-linux-amd64 / -arm64   (arm64 = QNAP / Raspberry Pi)
#   - bin/netlogger-darwin-arm64 / -amd64  (macOS)
#
# To bundle iperf3, drop iperf3.exe next to netlogger.exe (NetLogger prefers a
# co-located iperf3 over PATH).

$ErrorActionPreference = "Stop"
$env:Path += ";C:\Program Files\Go\bin"
New-Item -ItemType Directory -Force -Path bin | Out-Null

Write-Host "Building Windows GUI app (no console)..."
$env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -ldflags "-H windowsgui -s -w" -o bin/netlogger.exe ./cmd/netlogger

Write-Host "Building Windows console build..."
go build -ldflags "-s -w" -o bin/netlogger-cli.exe ./cmd/netlogger

Write-Host "Cross-compiling agents..."
$targets = @(
  @{os="linux";  arch="amd64"; out="bin/netlogger-linux-amd64"},
  @{os="linux";  arch="arm64"; out="bin/netlogger-linux-arm64"},
  @{os="darwin"; arch="arm64"; out="bin/netlogger-darwin-arm64"},
  @{os="darwin"; arch="amd64"; out="bin/netlogger-darwin-amd64"}
)
foreach ($t in $targets) {
  $env:GOOS = $t.os; $env:GOARCH = $t.arch
  go build -ldflags "-s -w" -o $t.out ./cmd/netlogger
  Write-Host ("  " + $t.out)
}

Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
Write-Host "Done. Release artifacts in bin/"
