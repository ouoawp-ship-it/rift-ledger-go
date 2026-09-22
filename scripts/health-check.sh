#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
url="http://127.0.0.1:${HOST_PORT:-8080}/healthz"
curl --fail --silent --show-error --max-time 5 "$url"
printf '\n容器状态：\n'; docker compose ps
if [[ -f backups/recovery/status.json ]]; then
  python3 scripts/recovery.py status
fi
