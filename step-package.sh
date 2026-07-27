#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

./step-package-desktop.sh
./step-package-android.sh

echo "Packages written to dist/desktop and dist/android"
