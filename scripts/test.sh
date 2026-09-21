#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export CGO_ENABLED=1
[[ -z "$(gofmt -l cmd internal pkg)" ]] || { echo "存在未按gofmt格式化的文件" >&2; exit 1; }
go vet ./...
go test -race -count=1 -cover ./...
python3 scripts/reliability_smoke.py
if command -v node >/dev/null 2>&1; then
  node --test scripts/api_client_test.cjs scripts/bot_connection_test.cjs
else
  echo "跳过前端测试：未安装 Node.js；可单独运行 node --test scripts/*_test.cjs"
fi
