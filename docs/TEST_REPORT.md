# Go 服务端 V0.1.0 验收记录

交付日期：2026-09-20。所有测试使用隔离数据，没有接触旧C#数据库、真实资金或真实Telegram会话。

## 实际执行结果

| 验收项 | 结果 |
|---|---|
| `go build ./cmd/server` | 通过；Linux amd64可启动 |
| `go vet ./...` | 通过 |
| `go test -count=1 ./...` | 50个顶层测试通过；含子测试累计93个通过事件 |
| `go test -race -count=1 -cover ./...` | 通过；本次执行未报告数据竞争 |
| 牛型穷举 | 全部100,000个五位数字向量，两种零组合政策均与独立枚举判定核对 |
| V9四组账本样例 | 898、817、1299、899均符合预期 |
| SQLite事务/幂等/冻结 | 回滚、并发重复命令、快照、所有庄位、费用组合、账目对平用例通过 |
| Telegram业务/HTTP模拟 | 入站去重、白名单、旧消息/群消息/编辑消息排除、卡片复用、429、403、503与未知状态用例通过 |
| 真实服务器进程HTTP验收 | 启动、鉴权、资源、计算、开盘、下注、封盘、预览、结算、下一期通过 |
| 重复结算与重启 | 没有增加重复流水；SIGTERM退出与重启后结果/下一期/对账保持 |
| 同目录第二实例 | 被单进程锁拒绝 |
| 前端JavaScript语法 | `node --check`通过，不等于浏览器功能验收 |
| Shell脚本语法 | 逐个`bash -n`通过 |
| 初始化配置 | 随机64位十六进制、文件600权限、拒绝覆盖既有配置通过 |
| Compose配置 | YAML能解析、宿主绑定回环地址已核对；未执行Docker Compose |

环境：`go version go1.23.2 linux/amd64`，实际SQLite `3.46.1`。没有将本地旧工具链编译的测试二进制作为生产发布文件；交付源码供目标环境重新构建。

## 实际HTTP全流程

创建新库→默认规则未确认、开盘拒绝→显式确认规则→开通零余额玩家→运营方登记10000、玩家登记1000→手选3号庄→开始→管理员测试接口受理1号100本金→重复业务号只保留一笔→玩家冻结101、运营方冻结400→封盘拒绝新增→生成预览→改变时长的旧token被拒绝→正式结算→重复结算不新记账→自动生成空白下一期→重启验证。

结算输入：1200秒；五个伤害 `[12745,99999,12710,12737,12345]`，3号庄牛1、1号闲牛9。

最终玩家1299，运营方9700，费用账户1，全部冻结释放；external清算账户用于保持系统双边平衡。只读对账无差异。

这笔受理通过管理员HTTP测试接口完成，**不是实际Telegram玩家消息验收**。Telegram适配层的网络交互使用本机模拟HTTP服务，不冒充真正平台成功回执。

## 测试覆盖率

```text
riftledger/cmd/server		coverage: 0.0% of statements
ok  	riftledger/internal/app	1.300s	coverage: 64.6% of statements
ok  	riftledger/internal/httpapi	1.104s	coverage: 54.1% of statements
ok  	riftledger/internal/sqlite	1.016s	coverage: 65.3% of statements
ok  	riftledger/internal/telegram	1.034s	coverage: 49.2% of statements
ok  	riftledger/pkg/bull	2.433s	coverage: 91.8% of statements
```

这些数值是对应测试运行的语句覆盖率，不是功能完成百分比。主入口在Go包测试中显示0%，但另有独立服务器进程HTTP/重启脚本覆盖启动场景；两种统计没有合并。不把测试通过理解为零缺陷、安全审计通过或生产可用性保证。

## 明确没有通过或没有执行

浏览器完整流程已尝试，Chromium在首次导航本地测试服务时返回 `net::ERR_BLOCKED_BY_ADMINISTRATOR`。没有修改浏览器策略绕过限制，没有继续声称页面操作成功，没有生成假“运行截图”。可复测脚本为 `scripts/browser_e2e.py`，目前在本环境未验收通过。

Docker镜像构建/启动、systemd安装、目标服务器网络与访问权限、真实Telegram两个账号联调、备份恢复演练、压力与长期稳定性测试、磁盘满/断电故障注入、安全渗透检查和旧C#迁移都没有执行。Compose配置和Shell语法校验不能替代这些操作。

## 复现与原始记录

`bash scripts/test.sh`执行Go静态检查、race测试和真实HTTP隔离验收，需Linux、Go、gcc、SQLite开发库、Python3。浏览器验收还需Playwright及Chromium。

原始证据在 `docs/test-artifacts/`：go-tests.jsonl、go-race-coverage.txt、validation-summary.json、http-smoke.json、browser-attempt.json。运行脚本会更新本地对应验收记录；请区分这次交付的记录与之后目标环境运行记录。
