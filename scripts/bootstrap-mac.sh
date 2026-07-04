#!/usr/bin/env bash
# One-time prerequisite setup for building NetLogger on macOS.
# Idempotent — safe to re-run. Installs: Xcode Command Line Tools (clang +
# macOS SDK, required by Gio/cgo), Homebrew, Go (>= 1.26), and iperf3.
#
# Run once after transferring the repo:  ./scripts/bootstrap-mac.sh
set -uo pipefail

say()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[33m!! %s\033[0m\n' "$*"; }

# 1. Xcode Command Line Tools — provides clang + the macOS SDK that Gio's
#    Cocoa/Metal backend links against. Without this there is no mac build.
if xcode-select -p >/dev/null 2>&1; then
  say "Xcode Command Line Tools: present ($(xcode-select -p))"
else
  say "Installing Xcode Command Line Tools (a GUI dialog will appear)..."
  xcode-select --install || true
  warn "Complete the CLT installer dialog, then re-run this script."
  exit 1
fi

# 2. Homebrew — used to install Go + iperf3.
if command -v brew >/dev/null 2>&1; then
  say "Homebrew: present ($(brew --version | head -1))"
else
  say "Installing Homebrew..."
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  [ -x /opt/homebrew/bin/brew ] && eval "$(/opt/homebrew/bin/brew shellenv)"
  [ -x /usr/local/bin/brew ]    && eval "$(/usr/local/bin/brew shellenv)"
fi

# 3. Go >= 1.26 (go.mod requires 1.26).
need_go=1
if command -v go >/dev/null 2>&1; then
  ver=$(go version | awk '{print $3}' | sed 's/go//')
  major=$(echo "$ver" | cut -d. -f1); minor=$(echo "$ver" | cut -d. -f2)
  if [ "$major" -gt 1 ] || { [ "$major" -eq 1 ] && [ "$minor" -ge 26 ]; }; then
    say "Go: present (go$ver)"; need_go=0
  else
    warn "Go $ver is too old (need >= 1.26)."
  fi
fi
if [ "$need_go" -eq 1 ]; then
  say "Installing/upgrading Go via Homebrew..."
  brew install go 2>/dev/null || brew upgrade go
  ver=$(go version | awk '{print $3}' | sed 's/go//')
  major=$(echo "$ver" | cut -d. -f1); minor=$(echo "$ver" | cut -d. -f2)
  if [ "$major" -eq 1 ] && [ "$minor" -lt 26 ]; then
    warn "Homebrew Go is $ver (< 1.26). Install the latest pkg from https://go.dev/dl/ instead."
  fi
fi

# 4. iperf3 — only needed for the speed/stress tests. Monitoring works without it.
if command -v iperf3 >/dev/null 2>&1; then
  say "iperf3: present ($(iperf3 --version 2>&1 | head -1))"
else
  say "Installing iperf3..."
  brew install iperf3
fi

say "Prerequisites ready."
cat <<'EOF'

Next steps:
  1. Open this repo folder in Claude Code on this Mac.
  2. Paste the kickoff prompt from docs/MACOS-BUILD-KICKOFF.md, OR
     follow that doc to build by hand.
EOF
