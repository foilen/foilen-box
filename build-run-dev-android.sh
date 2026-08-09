#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo "Building"
./step-clean-compile.sh
./step-package-android.sh

echo "Starting APK install"
./install-dev-apk.sh
