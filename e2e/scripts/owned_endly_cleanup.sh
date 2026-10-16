#!/usr/bin/env bash
set -euo pipefail
for port in 29501 29502 29511 29512 29513 29514 29515 29516; do
 python3 "$(dirname "$0")/owned_endly_process.py" stop "$port"
done
