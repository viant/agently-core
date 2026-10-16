#!/usr/bin/env bash
set -euo pipefail
exec python3 "$(dirname "$0")/owned_endly_process.py" start "$@"
