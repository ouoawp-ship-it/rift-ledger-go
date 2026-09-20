# 部署、备份与恢复

## 环境变量

| 变量 | 作用 |
|---|---|
| ADMIN_TOKEN | 必填32—128个字母/数字/下划线/横线；初始化生成64位随机十六进制 |
| LISTEN_ADDR | 直接运行默认127.0.0.1:8080 |
| ALLOW_PUBLIC_HTTP | 非回环监听必须显式true；Compose仅容器内开启，宿主机仍只绑定回环 |
| DATA_DIR | 默认./data；Compose固定/data |
| HOST_PORT | Compose宿主机回环端口，默认8080 |
| TG_BOT_TOKEN | 可选机器人Token；不写入数据库或日志 |
| TG_BOT_USERNAME | 启用机器人时必填，不带@，启动时getMe核实 |
| TG_GROUP_ID | 群负整数ID；空或0不推送群消息 |
| TG_TOPIC_ID | 0不指定话题；正整数为message_thread_id |
| TG_ADMIN_ID | 收到首次未开通玩家通知的个人正整数ID，可不填 |
| TG_SUPPORT_USERNAME | 私聊联系管理员的用户名，可不填 |

服务不实现代理配置界面；Go HTTP客户端遵守其常规环境代理设置，但请先在你的目标网络核验可访问Telegram。不要把测试Token、数据目录或整份.env公开上传。

## Docker

按根目录README执行。构建指定Go1.27.1-bookworm，运行镜像为debian:bookworm-slim，并安装系统SQLite。首次需要网络；没有第三方Go module下载。Docker镜像构建与运行未在本交付环境执行，镜像tag、镜像拉取、系统包源、架构与卷权限须在目标环境验证。

健康探针仅表示HTTP与数据库能响应，**不代表Telegram在线或有权限发送**。查看后台「运行检查」及容器日志中的Telegram状态。配置变更后用 `docker compose up -d` 重建/更新容器；密钥轮换不会修改数据库账目。

## systemd（二选一，不要与Docker同时启动同一Token）

先安装Go及README列出的本机构建依赖。在项目目录构建：

```bash
mkdir -p bin
CGO_ENABLED=1 go build -trimpath -o bin/rift-ledger-go ./cmd/server
sudo install -m 0755 bin/rift-ledger-go /usr/local/bin/rift-ledger-go
# .env必须先按README生成、编辑，不能复制.env.example当作正式配置
sudo install -m 0600 .env /etc/rift-ledger-go.env
sudo install -m 0644 deploy/rift-ledger-go.service /etc/systemd/system/rift-ledger-go.service
sudo systemctl daemon-reload
sudo systemctl enable --now rift-ledger-go
sudo systemctl status rift-ledger-go
sudo journalctl -u rift-ledger-go -n 100 --no-pager
```

只针对这份新服务名；检查已有同名文件后再安装，不覆盖不明旧服务。状态目录由systemd管理，使用DynamicUser与StateDirectory，实际存储可能对应 `/var/lib/private/rift-ledger-go`，请从服务配置核实，不猜路径并移动旧数据库。此systemd单元同样未在本环境执行。

## 一致性备份

`bash scripts/backup-docker.sh` 调用SQLite `.backup` 创建一致性快照，检查integrity_check，再复制到宿主机backups。无需停止整个服务，但过大的库可能有额外I/O开销，尚未压测。

备份含玩家ID、流水与消息正文，应限制访问并加密存放。保留多个时间点，不要将数据库备份和管理员Token一同公开。需要定时备份时，可由你在服务器配置计划任务；本项目不会擅自注册计划或删除旧备份。

## Docker从备份恢复（高影响操作，务必先确认对象）

恢复会丢掉备份时刻之后的账目，可能重复投递备份中的待发送消息。先停旧消费者，保留目前的数据与日志，核对备份与实际业务时间；这是人工维护，不是“撤销某笔单”的替代手段。

先用独立SQLite工具检查待恢复文件，再执行停机后的恢复。下面路径以 `backups/待恢复.db` 为例：

```bash
# 先创建当前一致性备份并妥善保存，不要在数据库严重损坏时盲目覆盖！
bash scripts/backup-docker.sh
docker compose stop app
# 只运行离线维护容器，共享同一个命名卷；不启动机器人与HTTP。
docker compose run --rm --no-deps -v "$PWD/backups:/restore:ro" --entrypoint sh app
```

在维护容器中先运行 `sqlite3 /restore/待恢复.db 'PRAGMA integrity_check;'`，确认输出ok与实际版本一致。将 `/data/rift-ledger.db`、对应 `-wal`、`-shm` 移入一个新建的受限现场目录，不删除；然后复制经验证的备份为 `/data/rift-ledger.db`，权限设600。只有完全停止写入后才能移动这些文件。检查所有者为当前容器运行用户，退出维护容器。

先以 **不启动真实Telegram的隔离验证环境** 检查恢复库的结果、账本与待发送消息，避免回滚offset/outbox带来消息重发；确认后再恢复正式Token。恢复流程未在此环境演练，不提供未经核对的破坏性一键恢复脚本。

## 运行限制

仅Linux，CGO_ENABLED=1，单SQLite连接串行事务/查询。本版追求清楚的事务边界与可复测，不声称无限并发；大账本分页、对账、长历史、磁盘满、断电/网络波动须在正式环境验收。不要多副本部署、不要使用NFS或复制Token启动第二接收器。

不要删除仍在运行进程的数据目录或锁文件；flock是针对同一文件的锁，删掉锁文件再开实例会破坏保护。
