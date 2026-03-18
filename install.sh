#!/bin/sh
# mdviewer installer — https://github.com/roraja/markdown-go
# Usage: curl -fsSL https://raw.githubusercontent.com/roraja/markdown-go/master/install.sh | sh
set -e

REPO="roraja/markdown-go"
BINARY="mdviewer"
INSTALL_DIR="/usr/local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7*|armhf) ARCH="armv7" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  linux) PLATFORM="linux" ;;
  darwin) PLATFORM="macos" ;;
  *) echo "Unsupported OS: $OS (use the PowerShell installer for Windows)"; exit 1 ;;
esac

# Get latest release tag
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
if [ -z "$LATEST" ]; then
  echo "Failed to fetch latest release"; exit 1
fi

ASSET="${BINARY}-${PLATFORM}-${ARCH}"
[ "$PLATFORM" = "darwin" ] && ASSET="${BINARY}-${PLATFORM}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${LATEST}/${ASSET}"

echo "Installing mdviewer ${LATEST} (${PLATFORM}/${ARCH})..."
TMP=$(mktemp)
curl -fsSL "$URL" -o "$TMP"
chmod +x "$TMP"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "${INSTALL_DIR}/${BINARY}"
else
  echo "Need sudo to install to ${INSTALL_DIR}"
  sudo mv "$TMP" "${INSTALL_DIR}/${BINARY}"
fi

echo "✅ mdviewer ${LATEST} installed to ${INSTALL_DIR}/${BINARY}"
echo "   Run: mdviewer -root ~/your-notes -port 8080"
