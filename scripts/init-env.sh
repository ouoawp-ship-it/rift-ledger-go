#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -e .env ]]; then
  echo "已有 .env，未覆盖任何密钥。请手动编辑现有配置。" >&2
  exit 1
fi
umask 077
key=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
[[ ${#key} -eq 64 ]] || { echo "随机密钥生成失败" >&2; exit 1; }
sed "s/^ADMIN_TOKEN=$/ADMIN_TOKEN=$key/" .env.example > .env
chmod 600 .env
echo "已创建 .env；请编辑 Telegram 配置。管理员登录密钥在 ADMIN_TOKEN 一行。"
echo "网页测试可暂时不填 Telegram；正式运营必须使用新的独立数据目录。"
