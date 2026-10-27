# 阶段 14：多实例与生产化

## 目标

把当前单进程 API 逐步提升为可水平扩展的部署：

```text
14.1 Redis Pub/Sub 共享通知和私信 SSE
14.2 Docker Compose 多 API 实例与 Nginx
14.3 Nginx 故障切换与 SSE 重连
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

## 14.2 双 API 与 Nginx（已完成）

### 部署结构

```text
浏览器 -> 宿主机 API_HOST_PORT -> Nginx -> api-1 / api-2
                                     least_conn
```

`api-1` 和 `api-2` 不直接发布宿主机端口，只在 Compose 内部暴露 `8080`。
Nginx 是唯一入口，Prometheus 分别抓取两个 API：

```text
api-1:8080/metrics
api-2:8080/metrics
```

### 关键代理配置

```text
least_conn                 长连接存在时优先选择活跃连接更少的副本
proxy_buffering off        SSE 事件立即下发
proxy_request_buffering off 上传流及时转给 API
proxy_read_timeout 1h      避免 SSE 被默认 60 秒超时切断
client_max_body_size 512m  允许视频和分片上传
X-Real-IP / X-Forwarded-For 传递真实客户端地址
X-Request-ID               贯通 Nginx 与 Go 日志
max_fails / fail_timeout   单副本异常时暂时摘除
```

API 默认仍不信任任何代理。Compose 显式注入：

```text
HTTP_TRUSTED_PROXIES=172.16.0.0/12
```

只因为 API 已不再直接发布宿主机端口，才允许信任 Docker 私网中的 Nginx 转发。

### 验证结果

```text
docker compose ps
  api-1 healthy
  api-2 healthy
  nginx healthy

nginx -t
  配置语法通过

12 个请求
  api-1 / api-2 交替收到请求

Prometheus targets
  api-1:8080 up
  api-2:8080 up

真实客户端 IP
  Nginx 后 API 日志 client_ip=198.51.100.77

跨实例 SSE
  SSE 落在 172.18.0.8
  关注写请求落在 172.18.0.2
  浏览器仍收到 follow 通知
```

## 14.3 Nginx 故障切换与 SSE 重连（已完成）

### 发现的问题

最初 Nginx 使用静态上游：

```text
server api-1:8080;
server api-2:8080;
```

停止 `api-2` 后，Docker DNS 中不再存在该名称。运行中的 Nginx 仍保留旧地址，
但执行 `nginx -t` 或 reload 会因为 `host not found in upstream` 失败。

同时，第一次故障请求等待了 5 秒代理连接超时，虽然最终能够切换，但用户体验不好。

### 修复

```text
resolver 127.0.0.11 valid=5s
server api-1:8080 resolve
server api-2:8080 resolve
proxy_connect_timeout 1s
proxy_next_upstream_tries 2
proxy_next_upstream_timeout 3s
```

这样单副本不存在时：

```text
Nginx 配置仍可检查和 reload
请求快速转到存活副本
容器恢复后由 DNS 重新发现并自动重新加入
```

### 验证结果

```text
停止 api-2
  20/20 请求返回 200
  全部 upstream 指向 api-1

启动 api-2，等待 12 秒
  20/20 请求返回 200
  请求在 api-1 和 api-2 之间交替

浏览器 SSE 场景
  先停止 api-2，确保 SSE 连接 api-1
  启动 api-2 后停止 api-1
  前端 3 秒退避后自动连接 api-2
  新关注通知到达浏览器，通知徽标从 0 变为 1
```

## 当前局限

```text
1. 限流仍是进程内额度，多实例后总容量会翻倍
2. Outbox 和浏览量回写 Worker 仍可能在多个实例同时运行，需要选主、分片或同等幂等约束
3. Redis 是实时通道的共享依赖，但 MySQL 数据不依赖 Redis Pub/Sub
4. 当前固定两个 API 服务，还没有自动扩缩容
```

## 下一步

```text
14.4 Redis 分布式令牌桶限流
```
