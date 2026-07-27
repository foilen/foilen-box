#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo ----[ Package: Desktop ]----
mkdir -p dist/desktop
go build -o dist/desktop/foilen-box ./cmd/foilenbox
tar -C dist/desktop -cJf dist/desktop/foilen-box.tar.xz foilen-box

echo "Package written to dist/desktop"
