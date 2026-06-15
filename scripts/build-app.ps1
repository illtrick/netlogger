# Build the portable, self-elevating NetLogger app (no console).
$ErrorActionPreference = "Stop"
$env:Path += ";C:\Program Files\Go\bin;$(go env GOPATH)\bin"
New-Item -ItemType Directory -Force -Path bin | Out-Null

Write-Host "Generating Windows manifest resource..."
go generate ./cmd/netlogger-app

Write-Host "Building NetLogger.exe (windowsgui, elevated)..."
$env:CGO_ENABLED = "0"
go build -ldflags "-H windowsgui -s -w" -o bin/NetLogger.exe ./cmd/netlogger-app

Write-Host "Done: bin/NetLogger.exe"
