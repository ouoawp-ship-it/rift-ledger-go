# 实现结构与一致性边界

Go标准库HTTP + 内嵌静态网页，业务经 `Service.Command` 进入一笔SQLite BEGIN IMMEDIATE事务。SQLite连接由进程内互斥锁串行访问，配置WAL、synchronous FULL和外键。此处不以“所有请求异步”代替数据库资金一致性。

核心表：settings（模板与版本）、accounts（余额/冻结/身份）、rounds（状态、英雄、庄位、规则快照、结果）、bets（受理事实与结算结果）、entries（双边不可变流水）、idempotency（成功请求响应）、audit（管理员/TG业务事件）、tg_updates（持久去重）、meta（offset/机器人身份）、outbox（待发送任务）、cards（群卡片message_id）。

每个账户更新都通过双边transfer，entries更新/删除有数据库触发器拒绝。外部人工调整对手方使用external清算账户，使全系统余额仍能对平。对账检查每批次两条且合计0、每个余额等于累计流水、全部账户余额总和0、冻结重建等于存储值。对账只是识别不一致，不自动“修复”余额。

结算流程：确认封盘→根据锁定快照重算预览→校验token→释放准备金→游戏与费用双边记账→保存每笔结算→保存期次结果→创建下一期空草稿→写待发任务→事务提交。任何失败回滚全部，不在数据库事务中等待Telegram网络请求。

Telegram处理：getUpdates拉取→验证私聊身份→同事务去重update、业务执行、回复入队与offset前移。拒绝下注通过savepoint撤回业务变化，拒绝记录与回复仍入库，重复同一被拒update不会以后变成一笔有效单。只处理message/private callback，edited_message不会重新下单；旧消息时间不晚于本期开盘秒时拒绝受理，开盘同一秒的边界采用保守拒绝。

发送队列每会话按id顺序推进，其他会话不依赖同一失败会话。网络结果不确定/5xx或崩溃遗留INFLIGHT转UNKNOWN，人工核验；429才按retry_after再次排队，明确4xx为FAILED。卡片已知message_id则编辑，收到“message is not modified”按已经完成处理。

**不能跨SQLite和Telegram提供真正的网络恰好一次发送保证。** 账本幂等与网络消息送达是两个问题；未知送达不自动重试的取舍是减少重复公示，但需要人工及时处理，否则本会话后续消息停住。管理员显式重试可能重复消息，但不会重复结算。

管理员共享随机密钥；SHA256固定长度常量时间比较，页面不存Token。只读HTTP/API无Cookie，不开放CORS，来源Host检查、CSP、输出转义。默认回环访问，SSH隧道或HTTPS反代。没有用户级RBAC、外部KMS、双人审批或安全渗透测试。

程序主入口使用Linux flock保护同一数据目录。这个锁不能防止你用另一个数据库或另一台服务器启动同一个TGToken，所以仍需人为停旧接收器。初始化与重启不重置倍率、玩家或已入账结果。

V0.1.0不是高可用集群系统；迁移到PostgreSQL、多租户、Webhook或多实例前，需要重新设计事务隔离、消费者分区、幂等范围和账本审计，不能只给Compose增加replicas。
