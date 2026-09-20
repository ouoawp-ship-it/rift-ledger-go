#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p backups; chmod 700 backups
name="rift-ledger-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
docker compose exec -T app sqlite3 /data/rift-ledger.db ".backup '/data/$name'"
docker compose cp "app:/data/$name" "backups/$name"
docker compose exec -T app rm -f "/data/$name"
chmod 600 "backups/$name"
sqlite3 "backups/$name" 'PRAGMA integrity_check;' | grep -qx ok
echo "备份完成：backups/$name"
