#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo ----[ Clean ]----
go clean ./...
rm -rf dist
rm -rf android/build android/app/build android/.gradle
rm -f android/app/libs/*.aar android/app/libs/*.jar
rm -rf internal/webserver/web/vendor-js internal/webserver/web/vendor-fonts
rm -f result
