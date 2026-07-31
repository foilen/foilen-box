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

# github.com/wlynxg/anet uses //go:linkname into unexported net internals
# (net.zoneCache) for cgo-free interface lookups on Android. Go's linker
# validates such references since Go 1.23 and this package's pinned
# reference no longer matches Go 1.26's internals, so relax the check.
GIT_COMMIT=$(git rev-parse --short HEAD)
GIT_COMMIT_DATE=$(TZ=UTC git log -1 --format=%cd --date=format-local:'%Y%m%d_%H%M')
"$GOMOBILE" bind -target=android -androidapi 21 -ldflags="-checklinkname=0 -X foilen-box/internal/webserver.Version=$GIT_COMMIT -X 'foilen-box/internal/webserver.CommitDate=$GIT_COMMIT_DATE'" -o android/app/libs/foilenbox.aar ./cmd/mobile
(
    cd android
    # Uses the Gradle wrapper if present (run `gradle wrapper` once inside
    # android/ to generate it), otherwise falls back to a system `gradle`.
    if [ -x ./gradlew ]; then
        ./gradlew assembleRelease
    else
        gradle assembleRelease
    fi
)
cp android/app/build/outputs/apk/release/app-release-unsigned.apk "$RUN_PATH/dist/android/Foilen_Box.apk"

echo "Package written to dist/android"
