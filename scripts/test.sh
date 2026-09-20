#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export CGO_ENABLED=1
[[ -z "$(gofmt -l cmd internal pkg)" ]] || { echo "存在未按gofmt格式化的文件" >&2; exit 1; }
go vet ./...
go test -race -count=1 -cover ./...
python3 scripts/smoke.py
