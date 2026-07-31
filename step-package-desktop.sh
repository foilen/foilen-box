#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo ----[ Package: Desktop ]----
mkdir -p dist/desktop
GIT_COMMIT=$(git rev-parse --short HEAD)
GIT_COMMIT_DATE=$(TZ=UTC git log -1 --format=%cd --date=format-local:'%Y%m%d_%H%M')
go build -ldflags="-X foilen-box/internal/webserver.Version=$GIT_COMMIT -X 'foilen-box/internal/webserver.CommitDate=$GIT_COMMIT_DATE'" -o dist/desktop/foilen-box ./cmd/foilenbox
tar -C dist/desktop -cJf dist/desktop/foilen-box.tar.xz foilen-box

echo "Package written to dist/desktop"
