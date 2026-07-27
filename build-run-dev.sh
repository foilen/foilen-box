#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo "Building"
./create-local-release.sh

echo "Starting APK install"
./install-dev-apk.sh 

echo "Starting desktop"
./start-dev-desktop.sh
