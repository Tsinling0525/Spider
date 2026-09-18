#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env.life
set +a
exec ./bin/lifed
