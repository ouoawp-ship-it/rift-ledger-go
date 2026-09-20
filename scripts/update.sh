#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
git fetch --prune
branch=$(git branch --show-current); [[ -n "$branch" ]] || { echo "当前不在分支" >&2; exit 1; }
git pull --ff-only
bash scripts/backup.sh
docker compose build
docker compose up -d
for i in {1..60}; do if curl -fsS --max-time 2 http://127.0.0.1:${HOST_PORT:-8080}/healthz >/dev/null; then echo "更新成功（分支：$branch）"; exit 0; fi; sleep 2; done
echo "更新后健康检查失败；保留容器和数据，请查看 docker compose logs --tail=200 app" >&2; exit 1
