# 阶段 14：多实例与生产化

## 目标

把当前单进程 API 逐步提升为可水平扩展的部署：

```text
14.1 Redis Pub/Sub 共享通知和私信 SSE
14.2 Docker Compose 多 API 实例
14.3 Nginx 负载均衡与 SSE 代理配置
14.4 Redis 分布式令牌桶限流
14.5 多实例集成测试、压测与故障演练
```

## 14.1 Redis Pub/Sub（已完成）

### 问题

原实现中的通知 Hub 和私信 Hub 都是进程内内存表：

```text
API-1: map[userID] -> 本地 SSE channel
API-2: map[userID] -> 本地 SSE channel
```

如果用户的 SSE 连接在 API-1，而业务请求被负载均衡到 API-2，API-2 无法直接找到
API-1 内存中的连接。

### 方案

```text
业务写入 MySQL
-> 构造 realtime.Envelope
-> publish 到 Redis 频道
-> 所有 API 实例的 Subscriber 收到事件
-> 每个实例检查本地 Hub
-> 只有持有目标用户 SSE 的实例真正推送
```

使用两个固定频道：

```text
feed:realtime:notifications
feed:realtime:messages
```

每条事件包含：

```text
id           事件唯一 ID
type         notification.created / message.created
recipient_id 目标用户
occurred_at  事件时间
payload      通知或私信 JSON
```

### 可靠性语义

Redis Pub/Sub 没有 ACK、消费确认和离线重放，因此它只承担在线加速：

```text
MySQL 是事实来源
SSE 是低延迟通知
Redis 发布失败时降级到当前实例本地 Hub
页面刷新或重新登录仍可从 MySQL 补齐
```

如果将来要求跨实例事件也不能丢，需要换 RabbitMQ、Kafka 或 Redis Streams，
而不是把通知事实只放在 Pub/Sub 里。

### 代码边界

```text
internal/realtime/event.go       事件协议与频道映射
internal/realtime/publisher.go   Redis Publish
internal/realtime/subscriber.go  订阅、重连、校验与 Handler 分发
cmd/api/realtime.go              把事件解码后交给通知/私信本地 Hub
pkg/metrics                      发布、接收、重连和连接状态指标
```

通知和私信 Service 只依赖 `realtime.Publisher` 接口。没有 Publisher 时保持原来的
单机 Hub 行为；Publisher 发布失败时回退到本地 Hub。

### 验证

```text
SSE 固定在 API-1 18080
关注与私信请求写入 API-2 18081
API-1 收到 1 个 follow 通知事件
API-1 收到 1 个 message 私信事件
realtime_subscriber_connected = 1
realtime_receive_total 成功计数增加
真实 Redis 发布订阅集成测试通过
```

### 与 C++ 聊天项目的对照

C++ 项目采用“Redis 保存用户所在 ChatServer + gRPC 定向调用目标节点”的路由模式：

```text
uip_<uid> -> ChatServer 地址
-> ChatGrpcClient.TextChatMsg(targetAddress)
```

Go 当前阶段采用“不知道目标连接在哪台机器，让所有实例一起接收事件”的广播模式：

```text
Redis Pub/Sub -> 所有 API -> 各实例筛选本地用户
```

两者都属于跨节点协调。前者适合明确目标、需要 RPC 响应的请求；后者适合在线通知、
私信提醒等不需要同步返回、可以容忍掉线后从数据库补查的事件。

## 当前局限

```text
1. 还没有启动双 API 容器和 Nginx
2. 限流仍是进程内额度，多实例后总容量会翻倍
3. Outbox Worker 仍可能在多个实例同时运行，需要选主、分片或幂等约束
4. Redis 是实时通道的共享依赖，但 MySQL 数据不依赖 Redis Pub/Sub
```

## 下一步

```text
14.2 Docker Compose 多 API 实例与 Nginx 负载均衡
```
