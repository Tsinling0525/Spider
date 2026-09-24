#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
exec data/device/asr-venv/bin/python scripts/local-device-asr.py
