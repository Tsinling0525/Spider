#!/bin/sh
# Local developer launcher. The private environment file is not tracked by Git.
set -eu
cd "$(dirname "$0")/.."
if [ -f data/device/local.env ]; then
    set -a
    . ./data/device/local.env
    set +a
fi
mkdir -p bin
go build -o bin/spider-device ./cmd/spider-device
exec ./bin/spider-device
