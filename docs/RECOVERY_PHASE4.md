# 第四阶段：单机恢复保障

用户目前没有第二台服务器。本阶段实现单机自动备份、可核验恢复包、隔离恢复副本和进程崩溃演练；不实现多机自动切换，也不宣称零丢失或全天候可用。业务结算、金额精度和数据库结构均不改变。

## 恢复目标和边界

- 默认每15分钟尝试备份，随机延迟最多30秒；保留最近96份成功核验的恢复包，正常情况下约24小时。备份耗时、失败或关机都会拉长实际恢复点间隔，不能把15分钟当作保证。状态命令按快照开始时间计算新鲜度，默认超过1小时失败退出。
- 同机恢复包可以应对误操作、损坏或程序故障，不能抵御整机/磁盘丢失。`status` 明确显示 `offsite:false`。尚无异机副本和整机恢复时间承诺。
- 恢复包包含密钥，只限管理员读取。SHA256用于发现意外损坏，不是抗篡改签名；本机未加密。以后迁往对象存储/其他主机前，应增加客户端加密、独立密钥保管及远端校验。
- 不自动启动另一个正式机器人，不自动覆盖正式库，不自动跳过/重放UNKNOWN消息。跨主机不能依靠本地flock防止双活。

## 恢复包内容

`backups/recovery/bundle-时间-随机ID/` 包含SQLite在线一致快照、`.env`副本、当前容器实际环境变量、运行中的机器人配置、英雄列表缓存（存在时）、Compose文件、只读账本检查结果和文件大小/SHA256清单。自定义消息图片、模板、账目、冻结、幂等记录、offset和outbox在数据库中。

备份前后比较容器ID、启动时间、实际环境、`.env`及运行配置；变化时失败，下一轮重试。数据库和配置没有跨文件事务，备份仍需在恢复时确认所用配置与业务时点；管理员不要在备份期间编辑部署文件。英雄图片可重新下载，不备份缓存图片或容器日志。恢复包记录Git提交和镜像ID，不包含源码、Go二进制或镜像层；请保留匹配的仓库和镜像，勿盲目执行 `docker image prune -a`。

每次备份先检查宿主机和数据卷可用空间：需要数据库逻辑大小3倍加512MiB余量。该检查不能预防同时发生的其他磁盘写入；写满等故障仍会失败退出。临时快照与恢复包分别需额外空间，持续保存96份时请按实际库大小预留容量。

发布过程：文件写入受限临时目录 → 独立无网络Docker容器检查账本 → 文件刷盘与清单校验 → 同文件系统原子重命名 → 保留策略 → 更新状态。并行任务使用flock互斥。仅本工具命名且文件校验通过的旧恢复包参与清理，至少保留3份；旧的单文件`.db`备份、未知目录、损坏或不完整恢复包不会自动删除。中断留下的 `.partial-*` 不算成功备份，确认无备份进程后可人工检查清理。首次备份失败不会启用定时器。

## 安装和日常检查（Docker + Linux）

要求Python3、Git、Docker Compose、systemd，以及第三阶段已包含 `rift-dbcheck` 的运行镜像。先在项目目录拉取新脚本；本阶段无需重建应用或重启机器人。

```bash
cd /opt/rift-ledger/rift-ledger-go
git pull --ff-only
bash scripts/install-recovery-timer.sh
python3 scripts/recovery.py status
systemctl list-timers rift-ledger-backup.timer --no-pager
```

安装器拒绝覆盖非本工具管理的同名单元。只设置备份定时器，采用root执行、umask077、较低CPU/I/O优先级。故障只写systemd日志及受限`status.json`，不会向Telegram发消息。没有独立外部监控时，不保证管理员会主动收到提醒。

```bash
# 手动备份；不同时间点的恢复包具有唯一名称
python3 scripts/recovery.py backup
# 默认检查最近尝试成功、恢复包文件完整且恢复点未超过1小时
python3 scripts/recovery.py status
# 查看失败原因；命令不会输出.env或Token内容
journalctl -u rift-ledger-backup.service -n 30 --no-pager
# 关闭定时器，不删任何备份
systemctl disable --now rift-ledger-backup.timer
```

旧的 `scripts/backup.sh` 和更新前备份继续保留原行为；`scripts/health-check.sh` 在发现恢复状态文件后也检查备份新鲜度。应用`/healthz`不受备份状态影响，避免因备份失败触发业务重启。

## 隔离恢复验证

先从`status`取得实际恢复包路径，替换以下示例；目标必须是一个尚不存在的新目录。

```bash
python3 scripts/recovery.py verify backups/recovery/实际恢复包目录
python3 scripts/recovery.py prepare backups/recovery/实际恢复包目录 /root/rift-isolated-review
cd /root/rift-isolated-review
docker compose up -d
docker compose exec -T review curl -fsS http://127.0.0.1:8080/healthz
docker compose down
```

`prepare`先重新检查全部文件和账本，复制到新目录，生成新的管理员密钥和禁用机器人的配置。生成的Compose无网络、无端口映射、无重启策略；原Token和实际环境不会进入可运行副本。不会修改原恢复包或正式数据，也不会启动服务，需执行上述`up`才运行。镜像ID必须在这台Docker主机上可用。

即使账本检查通过，仍须核对快照后发生的账目和消息。静态检查不能证明Telegram已经或尚未收到某一条消息；不能因UNKNOWN而批量重发。隔离副本仅供审查，不可直接修改网络配置后当正式服务使用。

## 正式故障处置和未来接管

1. **停止进一步变化并留证。** 暂停业务操作，保留当前库及WAL/SHM、日志、当前配置和镜像信息；不要先覆盖或删除现有数据。记录最后已确认的入账、结算、消息和备份时点。
2. **确认旧实例已被隔离。** 同机停止原容器并确认退出；跨机必须通过主机/网络控制明确切断旧实例。SSH不通不等于旧机器人已经停止。无法确认时，不启动新正式消费者；必要时通过BotFather轮换Token使旧实例失效，再将新Token只交给接管实例。
3. **独立恢复并对账。** 先执行verify/prepare，核对账本、余额、冻结、期次及备份后的人工审批、下注和结算。对无法确认的账目人工审查，禁止从聊天文本盲目自动补账。
4. **审查消息边界。** 检查快照中的PENDING/INFLIGHT/UNKNOWN、Telegram offset、实际群消息和个人回执。回退offset可能重新收到旧更新，回退outbox也可能重复发送已送达消息；幂等记录只覆盖快照内已有请求，不能保证备份后的事件自动去重。
5. **单实例恢复。** 仅在旧实例隔离、账目与消息边界确认后，人工选择最终库和配置并启动唯一正式实例。检查HTTP、接收状态、队列、对账，并用已授权账号确认查询不重复。保留事故前数据，禁止在不核对新账目的情况下回滚到更旧副本。

以后有第二个存储位置时，先补异机加密复制和远端回读校验，再测整机重建耗时；明确可接受数据损失和恢复时间后，才评估PostgreSQL及备用节点接管。当前不提供未经隔离确认的“一键切换”。

## 验证记录

- `python3 scripts/recovery_test.py`：覆盖成功恢复包、配置变化、容器重启、磁盘不足/写入失败、校验失败、保留策略、并发互斥、损坏/链接/越界文件、拒绝覆盖、恢复副本无Token/无网络、备份过期以及错误输出不泄露密钥。
- `python3 scripts/restore_drill.py`：真实HTTP创建测试账户及三位小数账目，恢复后保留余额1120.200和冻结100.001；拒绝相同数据目录的第二个进程；另提交0.001后SIGKILL，再启动核对账本和幂等重放。只使用临时库，不连接真实Telegram。
- 原始结果：[恢复工具故障注入](test-artifacts/phase4-recovery.txt)、[进程及账目恢复](test-artifacts/phase4-restore.json)。未进行真实主机断电、生产磁盘写满、跨机切换或24小时连续观察。

### 服务器验收（2026-09-22 11:08，北京时间）

已部署脚本版本 `7ad17c7`，启用 `rift-ledger-backup.timer`，状态为enabled/active。通过systemd手动触发实际备份单元，Result=success、ExecMainStatus=0，用时1.387秒；此耗时对应当前小库，不是大数据量承诺。安装时发现WorkingDirectory不能使用ExecStart式引号，已修正并增加含空格路径的真实systemd单元校验测试；17项恢复工具测试全部通过。

验收备份为 `backups/recovery/bundle-20260922T030800Z-d7106a945b9944c89474e5fab50a7f7c`，包含数据库、两类环境配置、运行配置、英雄列表、Compose和核验报告，schema6，账本检查通过。定时器下一次日历触发尚未在本次验收中等待，手动触发使用的就是同一service。

另用前一份实际恢复包生成 `/root/rift-phase4-review-20260922/`，启动无网络、无端口、机器人禁用的独立容器，实际HTTP对账4账户、14流水、总余额8198.443，balanced=true。核验后已停止并移除该隔离容器，保留副本和验收记录供检查。

正式应用启动时间仍为 `2026-09-22T02:51:14.815560398Z`，重启次数0；机器人online，队列无积压/待核实，正式对账一致。未更改正式账目或发送测试消息。服务器汇总报告位于 `/root/rift-phase4-acceptance.json`。第四阶段单机恢复保障已部署；异机备份、整机故障接管和长期观察仍未验收。
