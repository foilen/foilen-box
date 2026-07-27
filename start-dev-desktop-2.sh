#!/usr/bin/env bash
set -euo pipefail

echo "Starting desktop app..."

# Unset GTK/GIO/locale env vars leaked from the VS Code snap; they point at
# /snap/code/.../core20 libs whose libpthread is ABI-incompatible with the
# system glibc and crash the GTK-linked binary with a symbol lookup error.
unset GTK_PATH GTK_EXE_PREFIX GTK_IM_MODULE_FILE GIO_MODULE_DIR GSETTINGS_SCHEMA_DIR LOCPATH

export FOILEN_BOX_CONFIG_DIR="$(pwd)/_desktop_2"

./dist/desktop/foilen-box 2>&1 | tee _logs_2.txt
