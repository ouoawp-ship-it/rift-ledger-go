#!/usr/bin/env bash
# Read-only diagnostics. No bot token, player records or outgoing bot messages.
set -euo pipefail
cd "$(dirname "$0")/.."
printf '容器状态\n'
docker compose ps
printf '\n宿主机 DNS（IPv4）\n'
getent ahostsv4 api.telegram.org || true
printf '\n容器 DNS（IPv4）\n'
docker compose exec -T app getent ahostsv4 api.telegram.org || true
printf '\n宿主机 HTTPS 连通性（不带 Token）\n'
curl -4 -I --connect-timeout 10 --max-time 15 --silent --show-error https://api.telegram.org || true
printf '\n容器 HTTPS 连通性（不带 Token）\n'
docker compose exec -T app curl -4 -I --connect-timeout 10 --max-time 15 --silent --show-error https://api.telegram.org || true
printf '\n容器内后台健康\n'
docker compose exec -T app curl --fail --silent --show-error --max-time 5 http://127.0.0.1:8080/healthz || true
printf '\n队列状态与限流等待（只读，不含玩家明细）\n'
docker compose exec -T app sqlite3 -readonly /data/rift-ledger.db "SELECT state,COUNT(*) FROM outbox GROUP BY state; SELECT 'send_wait_seconds',MAX(0,CAST(value AS INTEGER)-CAST(strftime('%s','now') AS INTEGER)) FROM meta WHERE key='telegram_send_not_before'; SELECT 'oldest_pending_seconds',COALESCE(MAX(CAST(strftime('%s','now') AS INTEGER)-created_at),0) FROM outbox WHERE state IN ('PENDING','INFLIGHT');"
printf '\n说明：HTTPS 通只说明可以连接 Telegram；机器人身份、权限与具体送达请结合后台运行检查、发送记录判断。DNS 地址不同本身不能单独证明解析错误。\n'
