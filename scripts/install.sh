#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077
if [[ "$(uname -s)" != Linux* ]]; then echo "仅支持 Ubuntu/Linux" >&2; exit 1; fi
[[ "$(uname -m)" == x86_64 ]] || { echo "需要 x86_64" >&2; exit 1; }
if ! command -v docker >/dev/null 2>&1; then
  apt-get update
  apt-get install -y ca-certificates curl
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  . /etc/os-release
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" >/etc/apt/sources.list.d/docker.list
  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi
docker compose version >/dev/null || { echo "缺少Docker Compose plugin" >&2; exit 1; }
[[ -f .env ]] || bash scripts/init-env.sh
mkdir -p data backups
chmod 700 data backups
chmod 600 .env
if grep -q '^ADMIN_TOKEN=$' .env; then echo "请先在.env设置ADMIN_TOKEN" >&2; exit 1; fi
docker compose build
docker compose up -d
for i in {1..60}; do if curl -fsS --max-time 2 http://127.0.0.1:${HOST_PORT:-8080}/healthz >/dev/null; then echo "部署成功：http://127.0.0.1:${HOST_PORT:-8080}/"; exit 0; fi; sleep 2; done
echo "服务未在120秒内通过健康检查；查看 docker compose logs --tail=100 app" >&2; exit 1
