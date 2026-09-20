#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077
mkdir -p backups
chmod 700 backups
name="rift-ledger-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
# SQLite .backup creates a consistent snapshot, including committed WAL content.
docker compose exec -T app sh -c "umask 077; mkdir -p /data/backups; sqlite3 -cmd '.timeout 10000' /data/rift-ledger.db \".backup '/data/backups/$name'\""
check=$(docker compose exec -T app sqlite3 "/data/backups/$name" 'PRAGMA integrity_check;')
[[ "$check" == "ok" ]] || { echo "备份完整性检查失败：$check" >&2; exit 1; }
docker compose cp "app:/data/backups/$name" "backups/$name"
chmod 600 "backups/$name"
echo "一致性备份完成：backups/$name"
echo "备份含玩家与账目数据，请加密并另存异机；.env需另行安全保管。"
