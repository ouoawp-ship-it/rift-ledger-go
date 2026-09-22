# HTTP接口 V0.1.0

全部 `/api/*` 均为**管理员权限**，不要把管理员密钥分发给普通玩家。玩家身份只由Telegram入站的真实From.ID确定；`POST /api/bets` 是受保护的管理/测试入口，不是公开允许传account_id冒充玩家的接口。

统一返回：成功 `{"ok":true,"data":...}`，失败 `{"ok":false,"error":"中文原因"}`。HTTP状态使用400/401/403/404/409/415/500。所有JSON请求用Content-Type: application/json。金额单位为积分，支持最多三位小数，可传 JSON 数字或十进制字符串（如 `"1000.199"`）；超过三位小数、科学计数法和超限金额会被拒绝。ID、位置等仍为整数，伤害使用字符串，不接受未知字段或多对象请求。请求体上限1MiB。

```bash
# 从可信.env加载密钥；不要写死在共享脚本里
set -a; source .env; set +a
curl -sS http://127.0.0.1:8080/api/calculate \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"damage":"12745","banker_damage":"12710"}'
```

该请求只计算，不创建账目。返回hand、banker、comparison、player_win_multiplier。已确认规则仍按本期快照结算，独立计算器读取当前模板，不得把当前计算器结果当作旧期的权威复算。

## 幂等与预览

所有修改接口需 `Idempotency-Key`，8—160字符。相同业务重试保留原键和完全相同的输入；复用键改输入返回409。服务持久化成功命令的输入指纹和响应，重复命令不再修改账目。失败命令回滚不收费，不缓存失败为成功。

唯一例外：计算和预览不是写入，不要求幂等键。结算时还必须携带当前预览的 `token` 到 `preview_token`。修改伤害后，重新请求预览；禁止复用旧预览去解释新输入。

网页的未知网络结果保留该输入的业务键；刷新页面后内存键可能丢失，因此未知结果必须先查账，不要换键盲目再次调分。API调用方应在自己本地持久化每次业务键。

## 接口表

运行观测：`GET /api/operations` 返回 `checked_at`、`receiver`、`business`、`sender`、`database`，需要管理员鉴权。`receiver.receiver_state` 独立表示接收状态，`receiver.state` 为综合状态；`sender` 含 `pending`、`inflight`、`needs_review`、`oldest_age`（秒）；`database` 含外层操作次数及累计/最大等待、执行毫秒数。业务处理和耗时计数从本次启动开始，不能当作历史持久统计。`GET /api/bot-connection` 保留原字段，并附带这些分项状态。

发送429后，`sender.retry_at` 为持久化的预计恢复Unix秒，无等待时为0。没有更高优先级的待核实问题时，`sender.state` 为 `rate_limited`；有待核实消息时仍为 `blocked`，同时保留 `retry_at`。接收正常但发送限流时综合状态为 `degraded`。

第三阶段：`operations.audit_database` 为完整对账只读连接的独立耗时/等待计数；`database` 仍为业务连接。`GET /api/reconcile` 在一致的WAL读快照中核对，不占用业务连接互斥锁。`state.bets` 仍是最近200笔、按时间/id升序展示，但现在直接在SQL中限量。玩家今日盈亏改读与流水事务同步的北京时间日汇总，金额和统计口径不变。

| 方法/路径 | 输入或说明 |
|---|---|
| GET /healthz | 无鉴权，仅DB/HTTP健康，不代表TG在线 |
| GET /api/state | 版本、实际TG状态、当前规则/期次、最近200注单、待发计数 |
| GET /api/accounts | 管理账户列表，不返回external清算账户 |
| GET /api/bets?account_id=tg:123 | 必须指定查询账户，否则返回空列表 |
| GET /api/rounds | 历史列表，包含草稿 |
| GET /api/rounds/{id} | 指定期次及保存的结算事实 |
| GET /api/entries?account_id=house | 流水；account_id为空表示全部 |
| GET /api/audit | 操作日志 |
| GET /api/outbox | 发送记录 |
| GET /api/reconcile | 只读检查账本及冻结 |
| POST /api/calculate | damage、可选banker_damage字符串 |
| POST /api/rules | expected_version与rules完整对象 |
| POST /api/accounts | telegram_id整数、name、enabled |
| POST /api/adjustments | account_id、delta非零积分金额（最多三位小数）、note必填 |
| POST /api/rounds | number、banker、heroes；允许banker0和heroes[]创建空草稿 |
| POST /api/rounds/{id}/configure | number、banker1..5、heroes五项{id,name} |
| POST /api/rounds/{id}/open | 空对象{}；必须先确认规则与英雄 |
| POST /api/rounds/{id}/close | 空对象{} |
| POST /api/bets | account_id、round_id、position1..5非庄位、stake正数积分金额（最多三位小数） |
| POST /api/rounds/{id}/preview | damages五个字符串（旧客户端可继续携带duration_seconds，但网页不再使用） |
| POST /api/rounds/{id}/settle | 上述输入 + preview_token |
| POST /api/outbox/{id}/resolve | action=retry/ack/skip；ack卡片需真实message_id |

列表支持 `limit`（默认50，上限200）及 `offset`。accounts/bets/rounds是类型化结构；entries/audit/outbox是数据库明细，**数值字段在JSON中可能是十进制字符串**，调用方不可假定所有字段都是number。时间戳为Unix秒，网页/TG按北京时间显示。

规则示例（仅是测试选择，生产应由操作者确认）：

```json
{"expected_version":1,"rules":{"confirmed":true,"payout":[1,1,1,1,1,1,1,2,2,3,4],"min_stake":20,"max_stake":300,"fee_timing":"settlement","void_fee":"refund","fee_recipient":"fees","zero_triple":true}}
```

`expected_version` 必须从真实state读取，不硬编码1用于已经运行的库。可运行的完整接口调用样例见 `scripts/smoke.py`：它只建立临时数据库、随机密钥和回环端口，不读取.env或向真实Telegram发消息。
# 玩家历史查询

新增管理员只读接口 `/api/player-search` 与 `/api/player-history`，提供玩家搜索、按日期/结果筛选的下注、积分流水、上下分申请和游标分页。参数及时间/金额口径见 [玩家历史明细](PLAYER_HISTORY.md)。
