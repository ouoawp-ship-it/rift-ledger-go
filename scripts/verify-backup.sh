#!/usr/bin/env bash
# Isolated offline inspection; never mounts the live volume or loads .env.
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $# == 1 ]] || { echo "用法：bash scripts/verify-backup.sh backups/备份文件.db" >&2; exit 1; }
backup_file=$(realpath -e -- "$1")
[[ -f "$backup_file" ]] || { echo "必须指定数据库备份文件" >&2; exit 1; }
[[ ! -e "$backup_file-wal" && ! -e "$backup_file-journal" ]] || { echo "请选择 .backup 生成的独立快照，不接受旁边仍有WAL/日志的数据库" >&2; exit 1; }
[[ "$backup_file" != *,* ]] || { echo "备份路径不支持逗号" >&2; exit 1; }
image=$(docker compose images -q app)
[[ -n "$image" && "$image" != *$'\n'* ]] || { echo "请先构建包含 rift-dbcheck 的 app 镜像" >&2; exit 1; }
docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
  --user "$(id -u):$(id -g)" --tmpfs /tmp:rw,nosuid,nodev,size=128m \
  --mount "type=bind,src=$backup_file,dst=/backup.db,readonly" \
  --entrypoint /usr/local/bin/rift-dbcheck "$image" -db /backup.db
echo "备份只读验证通过；未启动机器人、未覆盖正式数据库。恢复时仍需核实备份时点之后的消息和账目。"
