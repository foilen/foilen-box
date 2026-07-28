#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo ----[ Package: Desktop ]----
mkdir -p dist/desktop
GIT_COMMIT=$(git rev-parse --short HEAD)
go build -ldflags="-X foilen-box/internal/webserver.Version=$GIT_COMMIT" -o dist/desktop/foilen-box ./cmd/foilenbox
tar -C dist/desktop -cJf dist/desktop/foilen-box.tar.xz foilen-box

echo "Package written to dist/desktop"
