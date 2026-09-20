#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p backups; chmod 700 backups
name="rift-ledger-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
docker compose exec -T app sqlite3 /data/rift-ledger.db ".backup '/data/$name'"
check=$(docker compose exec -T app sqlite3 "/data/$name" 'PRAGMA integrity_check;')
[[ "$check" == "ok" ]] || { echo "备份完整性检查失败：$check" >&2; exit 1; }
docker compose cp "app:/data/$name" "backups/$name"
docker compose exec -T app rm -f "/data/$name"
chmod 600 "backups/$name"
echo "备份完成：backups/$name"
