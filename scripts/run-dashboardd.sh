#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
set -a
if [ -f .env.dashboard ]; then . ./.env.dashboard; fi
set +a
exec ./bin/dashboardd
