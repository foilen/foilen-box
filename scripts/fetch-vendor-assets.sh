#!/bin/bash
# Fetches all vendor web assets (fonts, JS) at build time so the web UI
# never hits an external CDN at runtime. Both sub-scripts skip work that's
# already present, so this is a no-op on repeat builds unless the relevant
# vendor-* directory is deleted.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
WEB_DIR="$REPO_ROOT/internal/webserver/web"

echo "Fetching vendor assets for offline use..."

bash "$SCRIPT_DIR/fetch-vendor-fonts.sh" "$WEB_DIR/vendor-fonts"
node "$SCRIPT_DIR/fetch-vendor-js.mjs" "$WEB_DIR/vendor-js"

echo ""
echo "✓ All vendor assets fetched successfully"
