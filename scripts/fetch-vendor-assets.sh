#!/bin/bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
WEB_DIR="$REPO_ROOT/internal/webserver/web"

echo "Fetching vendor assets for offline use..."

bash "$SCRIPT_DIR/fetch-vendor-fonts.sh" "$WEB_DIR/vendor-fonts"
node "$SCRIPT_DIR/fetch-vendor-js.mjs" "$WEB_DIR/vendor-js"

echo ""
echo "✓ All vendor assets fetched successfully"
