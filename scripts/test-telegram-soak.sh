#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
seconds="${1:-300}"
if [[ ! "$seconds" =~ ^[0-9]+$ ]] || (( ${#seconds} > 3 )) || (( 10#$seconds < 60 || 10#$seconds > 600 )); then
  printf '用法：bash scripts/test-telegram-soak.sh [60至600秒，默认300]\n' >&2
  exit 2
fi
export CGO_ENABLED=1
RIFT_SOAK_SECONDS="$seconds" go test -race ./internal/telegram -run '^TestTelegramSoak$' -count=1 -v -timeout 13m
