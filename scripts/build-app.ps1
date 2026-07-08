# Build the portable, self-elevating NetLogger app (no console).
$ErrorActionPreference = "Stop"
$env:Path += ";C:\Program Files\Go\bin;$(go env GOPATH)\bin"
New-Item -ItemType Directory -Force -Path bin | Out-Null

Write-Host "Generating Windows manifest resource..."
go generate ./cmd/netlogger-app

Write-Host "Building NetLogger.exe (windowsgui, elevated)..."
$env:CGO_ENABLED = "0"
$build = (git rev-parse --short HEAD 2>$null)
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($build)) { $build = "dev" }
# Dirty = modified TRACKED files only; untracked files don't change the binary.
if ((git status --porcelain --untracked-files=no 2>$null)) { $build = "$build-dirty" }
$ldflags = "-H windowsgui -s -w -X netlogger/internal/version.Build=$build"
go build -ldflags $ldflags -o bin/NetLogger.exe ./cmd/netlogger-app

Write-Host "Done: bin/NetLogger.exe (build $build)"
