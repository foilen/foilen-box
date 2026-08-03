#!/bin/bash

set -e

RUN_PATH="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd $RUN_PATH

echo ----[ Fetch Vendor Assets ]----
./scripts/fetch-vendor-assets.sh

echo ----[ Compile ]----
go build ./...
go test ./...
