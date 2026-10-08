#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ ! -f .env.xiaozhi ]; then
  echo 'Copy .env.xiaozhi.example to .env.xiaozhi and configure MCP_ENDPOINT first.' >&2
  exit 1
fi
set -a
if [ -f .env.dashboard ]; then . ./.env.dashboard; fi
. ./.env.xiaozhi
set +a
exec ./bin/xiaozhi-bridge
