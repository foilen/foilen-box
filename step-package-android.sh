#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

export PATH="$(go env GOPATH)/bin:$PATH"
GOMOBILE="$(go env GOPATH)/bin/gomobile"
if [ ! -x "$GOMOBILE" ]; then
    echo "gomobile not found, installing (go install golang.org/x/mobile/cmd/gomobile@latest)..."
    go install golang.org/x/mobile/cmd/gomobile@latest
    go install golang.org/x/mobile/cmd/gobind@latest
    "$GOMOBILE" init
fi

echo ----[ Package: Android ]----
mkdir -p dist/android android/app/libs

GIT_COMMIT=$(git rev-parse --short HEAD)
GIT_COMMIT_DATE=$(TZ=UTC git log -1 --format=%cd --date=format-local:'%Y%m%d_%H%M')
"$GOMOBILE" bind -target=android -androidapi 21 -ldflags="-checklinkname=0 -X foilen-box/internal/webserver.Version=$GIT_COMMIT -X 'foilen-box/internal/webserver.CommitDate=$GIT_COMMIT_DATE'" -o android/app/libs/foilenbox.aar ./cmd/mobile
(
    cd android
    if [ -x ./gradlew ]; then
        ./gradlew assembleRelease
    else
        gradle assembleRelease
    fi
)
cp android/app/build/outputs/apk/release/app-release-unsigned.apk "$RUN_PATH/dist/android/Foilen_Box.apk"

echo "Package written to dist/android"
