#!/usr/bin/env bash
set -euo pipefail

echo "Starting desktop app..."

unset GTK_PATH GTK_EXE_PREFIX GTK_IM_MODULE_FILE GIO_MODULE_DIR GSETTINGS_SCHEMA_DIR LOCPATH

export FOILEN_BOX_CONFIG_DIR="$(pwd)/_desktop_1"

./dist/desktop/foilen-box 2>&1 | tee _logs_1.txt
