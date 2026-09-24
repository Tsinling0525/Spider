#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env.llm
set +a
exec ./bin/llmd
