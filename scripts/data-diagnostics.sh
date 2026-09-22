#!/usr/bin/env bash
# Counts and file sizes only. No account IDs, messages, tokens, writes or checkpoints.
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose exec -T app sqlite3 -readonly -json /data/rift-ledger.db "
SELECT sqlite_version() AS sqlite_version,(SELECT value FROM meta WHERE key='schema_version') AS schema_version;
SELECT (SELECT COUNT(*) FROM accounts WHERE role='player') AS players,
 (SELECT COUNT(*) FROM rounds) AS rounds,(SELECT COUNT(*) FROM bets) AS bets,
 (SELECT COUNT(*) FROM entries) AS entries,(SELECT COUNT(*) FROM outbox) AS outbox;
SELECT state,COUNT(*) AS count FROM outbox GROUP BY state;
PRAGMA page_size;
PRAGMA page_count;
PRAGMA freelist_count;"
docker compose exec -T app sh -c 'for f in /data/rift-ledger.db /data/rift-ledger.db-wal /data/rift-ledger.db-shm; do if [ -f "$f" ]; then stat -c "%n %s bytes" "$f"; fi; done'
