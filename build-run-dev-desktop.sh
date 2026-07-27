#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo "Building"
./step-clean-compile.sh
./step-package-desktop.sh

echo "Starting desktop"
./start-dev-desktop.sh
