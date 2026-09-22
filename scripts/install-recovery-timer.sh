#!/usr/bin/env bash
# Install only the recovery timer; does not restart app or alter its data.
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $(id -u) == 0 ]] || { echo "请以root安装systemd备份定时器" >&2; exit 1; }
command -v python3 >/dev/null
command -v systemctl >/dev/null
for unit in rift-ledger-backup.service rift-ledger-backup.timer; do
  if [[ -e /etc/systemd/system/$unit ]] && ! head -n 1 "/etc/systemd/system/$unit" | grep -qx '# Managed by Rift Ledger recovery'; then
    echo "拒绝覆盖现有非本项目单元：$unit" >&2; exit 1
  fi
done
# A working verified backup is a prerequisite for enabling the schedule.
python3 scripts/recovery.py backup
python3 - "$PWD" <<'PY'
import os
from pathlib import Path
import sys
root = sys.argv[1]
if any(c in root for c in '\n\r\x00'):
    raise SystemExit('项目路径不能含换行')
def quote(value):
    return '"' + value.replace('\\', '\\\\').replace('"', '\\"').replace('%', '%%') + '"'
service = '''# Managed by Rift Ledger recovery
[Unit]
Description=Rift Ledger verified recovery backup
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
User=root
UMask=0077
WorkingDirectory={working}
ExecStart=/usr/bin/python3 {script} backup --keep 96
TimeoutStartSec=30min
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=7
StandardOutput=journal
StandardError=journal
'''.format(working=quote(root), script=quote(root + '/scripts/recovery.py').replace('$', '$$'))
timer = '''# Managed by Rift Ledger recovery
[Unit]
Description=Rift Ledger backup every 15 minutes

[Timer]
OnCalendar=*-*-* *:00/15:00
RandomizedDelaySec=30s
Persistent=true
Unit=rift-ledger-backup.service

[Install]
WantedBy=timers.target
'''
for name, text in [('rift-ledger-backup.service', service), ('rift-ledger-backup.timer', timer)]:
    target = Path('/etc/systemd/system') / name
    temp = target.with_suffix(target.suffix + '.tmp')
    with temp.open('x') as f:
        os.chmod(temp, 0o644)
        f.write(text)
        f.flush()
        os.fsync(f.fileno())
    os.replace(temp, target)
PY
systemd-analyze verify /etc/systemd/system/rift-ledger-backup.service /etc/systemd/system/rift-ledger-backup.timer
systemctl daemon-reload
systemctl enable --now rift-ledger-backup.timer
systemctl list-timers rift-ledger-backup.timer --no-pager
echo "已启用：每15分钟成套备份，保留最近96份已校验恢复包。原有单文件备份不删除。"
echo "状态检查：python3 scripts/recovery.py status；定时器日志：journalctl -u rift-ledger-backup.service -n 30 --no-pager"
