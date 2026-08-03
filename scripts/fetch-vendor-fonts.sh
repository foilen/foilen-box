#!/bin/bash
# Downloads Google Fonts (CSS + TTF files) to a local directory, rewriting
# the CSS to reference the local files, so the web UI never fetches fonts
# from fonts.googleapis.com/fonts.gstatic.com at runtime.
#
# Usage: fetch-vendor-fonts.sh <output-dir>

set -e

OUT_DIR="$1"
if [ -z "$OUT_DIR" ]; then
    echo "usage: fetch-vendor-fonts.sh <output-dir>" >&2
    exit 1
fi

if [ -f "$OUT_DIR/google-fonts.css" ]; then
    echo "Fonts already present at $OUT_DIR, skipping (delete the directory to refetch)"
    exit 0
fi

mkdir -p "$OUT_DIR"

echo "Downloading Google Fonts CSS and font files..."
curl -s -f "https://fonts.googleapis.com/css2?family=Roboto:wght@400;500;700&family=Material+Symbols+Outlined" -o "$OUT_DIR/google-fonts.css"

if [ ! -s "$OUT_DIR/google-fonts.css" ]; then
    echo "ERROR: Failed to download Google Fonts CSS"
    exit 1
fi

# Download font files referenced in the Google Fonts CSS
FONT_URLS=$(grep -oE "https://fonts\.gstatic\.com[^)]*" "$OUT_DIR/google-fonts.css" | sort -u)
FONT_COUNT=0
while IFS= read -r url; do
    if [ -z "$url" ]; then
        continue
    fi
    filename=$(basename "$url")
    curl -s -f "$url" -o "$OUT_DIR/$filename"
    FONT_COUNT=$((FONT_COUNT + 1))
    echo "  ✓ Downloaded font: $filename"
done <<< "$FONT_URLS"

echo "Downloaded $FONT_COUNT font files"

# Rewrite the CSS file to point to local font files (saved flat, by basename)
sed -i -E 's|https://fonts\.gstatic\.com/[^)]*/([^/)]+)|\1|g' "$OUT_DIR/google-fonts.css"

echo "✓ Fonts fetched"
