#!/usr/bin/env bash
set -euo pipefail

CURRENT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$CURRENT_SCRIPT_DIR"

echo "Building desktop package..."
nix build .#default

echo "Build complete. Installing to nix profile..."
nix profile remove foilen-box || true
nix profile add .#default

echo "Done. Run 'foilen-box' to use the app."
