#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ -f .env ]] || { echo "请先运行 bash scripts/init-env.sh" >&2; exit 1; }
# Source only your own trusted configuration file.
set -a
source .env
set +a
export DATA_DIR="${DATA_DIR:-./data}"
export LISTEN_ADDR="${LISTEN_ADDR:-127.0.0.1:8080}"
export CGO_ENABLED=1
exec go run ./cmd/server
