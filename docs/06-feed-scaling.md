# 阶段 6：Feed 写扩散、推拉结合与可靠性

## 目标

在阶段 5 的纯拉模式基础上，逐步加入：

1. Redis Feed 收件箱。
2. 视频发布事件和写扩散。
3. outbox 可靠投递。
4. RabbitMQ 异步消费。
5. 重试、幂等和死信队列。
6. 大 V 读时拉取与推拉结果合并。

每一阶段都必须保留纯拉模式作为降级路径。

## 当前完成情况

已完成：

- [x] Redis Feed Inbox 与 ZSET 稳定排序。
- [x] FollowRepository 粉丝分页。
- [x] FanoutService 与 Fake 测试。
- [x] MySQL 事务 Outbox。
- [x] RabbitMQ Producer Publisher Confirm。
- [x] RabbitMQ Consumer 手动 ACK。
- [x] Retry Exchange、TTL Retry Queue、DLX 和 DLQ。
- [x] 临时错误有限重试，永久错误直接进入 DLQ。
- [x] 大 V 跳过写扩散，改由读取时从 MySQL 拉取。
- [x] 推拉结果合并、去重和 `(created_at, id)` 稳定排序。
- [x] Redis 数据不足时从 MySQL 回填。
- [x] Redis 故障时回退到纯 MySQL 拉模式。
- [x] 大 V 作者分类 Redis 缓存及关注关系变更失效。
- [x] Consumer Supervisor 自动重连。
- [x] 独立 RabbitMQ Consumer Connector。
- [x] 连接故障注入和恢复集成测试。
- [x] DLQ 查询与重放 CLI。
- [x] Consumer、Outbox、DLQ 和 HTTP Prometheus 指标。

待完成：

- [ ] Feed Inbox 重建工具核心逻辑。

运维、健康检查、重连、DLQ 和指标细节见：

```text
docs/07-operations-observability.md
```

## 推拉模型

推模式：

```text
视频发布
-> 找到所有粉丝
-> 写入每个粉丝的 Redis 收件箱
```

拉模式：

```text
用户读取 Feed
-> 查询关注作者的视频
-> 使用复合游标分页
```

推拉结合：

```text
普通作者 -> 写扩散
大 V     -> 读时拉取
读取端   -> 合并两类结果并按 (created_at, id) 排序
```

## Redis 收件箱

```text
key:    feed:inbox:<userID>
member: videoID
score:  publishedAt 毫秒时间戳
```

基本操作：

```text
ZADD
ZREVRANGE
ZREMRANGEBYRANK
```

收件箱默认最多保留 1000 条。Redis 只保存视频 ID，视频元数据仍以 MySQL 为准。

## 写扩散流程

```text
1. 视频元数据写 MySQL
2. 同一事务写 outbox_event
3. outbox publisher 发送 video.published
4. RabbitMQ 投递给 fanout consumer
5. consumer 查询作者粉丝
6. 向粉丝 inbox 批量 ZADD
7. 裁剪 inbox
```

## 可靠性

- 重复消息：ZADD 对相同 member 天然幂等。
- MQ 发送失败：outbox 保持 pending 并重试。
- 消费失败：有限次重试后投递 DLQ。
- Redis 不可用：读取端降级为 MySQL 纯拉模式。
- 缓存丢失：从 MySQL 视频和关注关系重建 inbox。

## 当前任务

1. [x] 完成 `Inbox` 接口与 Redis ZSET 实现。
2. [x] 给 FollowRepository 增加分页 `ListFollowerIDs`。
3. [x] 完成 FanoutService 和 Fake 测试。
4. [x] 增加 outbox 表和后台 publisher。
5. [x] 接入 RabbitMQ producer 和 consumer。
6. [x] 增加 DLQ、重试和幂等测试。
7. [x] 实现大 V 拉取和推拉合并。
8. [x] 增加 Consumer Supervisor 和断线恢复。
9. [x] 增加 DLQ 运维工具。
10. [x] 增加健康检查分层和 Prometheus 指标。
11. [ ] 实现 Feed Inbox 重建工具。

## 面试追问

1. 为什么不能在上传 HTTP 请求里同步写所有粉丝收件箱？
2. 为什么需要 outbox，而不是 MySQL 写完直接发 MQ？
3. Redis ZSET 如何解决同时间视频的稳定排序？
4. 大 V 为什么不适合全量写扩散？
5. Redis 不可用时 Feed 如何降级？
6. RabbitMQ 重复消费时写扩散为什么仍然正确？
7. Consumer 断线后为什么不需要重启整个 API？
8. DLQ 重放为什么必须先发布成功再 ACK？
9. 为什么指标标签不能使用真实视频 ID？
