#!/usr/bin/env bash
set -euo pipefail

CURRENT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$CURRENT_SCRIPT_DIR"

echo "Building desktop package..."
nix build .#default
