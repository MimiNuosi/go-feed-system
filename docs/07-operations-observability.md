# 阶段 7：健康检查、重连、DLQ 与指标

阶段 6 完成了 Feed 主链路，但线上运行还需要回答三个问题：

```text
依赖挂掉时，服务会不会一起挂？
消息失败后，如何查看和恢复？
出了故障时，如何知道系统当前状态？
```

本阶段围绕这三个问题补齐运维能力。

## 1. 健康检查分层

健康检查分为：

```text
/livez  进程是否还活着
/readyz 当前依赖是否可用
```

依赖分为两类：

```text
critical dependency     MySQL
degradable dependency  Redis、RabbitMQ
```

MySQL 是事实源，不可用时 `/readyz` 返回 503。Redis 或 RabbitMQ 不可用时，
`/readyz` 仍返回 HTTP 200，但整体状态为 `degraded`，因为 Feed 可以退化到
MySQL 拉模式，视频上传仍能先写 MySQL 和 Outbox。

健康检查不向客户端返回底层错误，只服务端记录具体错误，避免泄露技术栈。

## 2. Consumer Supervisor

Consumer 不再由服务启动阶段固定建立。启动时只构造组件，运行后由 Supervisor
维护 Session：

```text
Connect
-> 声明 RabbitMQ 拓扑
-> 创建 Consumer Channel
-> 建立 RabbitConsumer
-> Consume
-> 失败后指数退避
-> 重新 Connect
```

退避范围：

```text
1s -> 2s -> 4s -> 8s -> ... -> 30s
```

RabbitMQ 不在线不会阻止 API 启动。Consumer 断线也不会停止 Outbox Worker。

实现位置：

```text
cmd/api/consumer_supervisor.go
cmd/api/rabbit_consumer_connector.go
cmd/api/feed_messaging.go
```

## 3. DLQ 运维工具

新增独立命令：

```text
cmd/dlqctl
```

查看 DLQ：

```powershell
go run ./cmd/dlqctl list -limit 20
```

预览重放：

```powershell
go run ./cmd/dlqctl replay -message-id <message-id>
```

真正重放：

```powershell
go run ./cmd/dlqctl replay -message-id <message-id> -execute
```

安全顺序是：

```text
读取 DLQ，autoAck=false
-> 发布到主 Exchange
-> 等待 Publisher Confirm
-> ACK 原消息
```

如果发布失败，原消息重新入队。重放保留 MessageId，并清理 `x-retry-count`
和 `x-death` 等 RabbitMQ 管理字段，避免消息立刻再次进入 DLQ。

## 4. Prometheus 指标

新增：

```text
pkg/metrics
internal/middleware/metrics.go
```

暴露接口：

```text
GET /metrics
```

当前指标：

```text
http_in_flight_requests
http_requests_total
http_request_duration_seconds

feed_consumer_connected
feed_consumer_reconnect_total
feed_consumer_dlq_total

outbox_publish_total
```

Prometheus 配置：

```text
deploy/prometheus/prometheus.yml
deploy/prometheus/alerts.yml
```

当前告警覆盖 API 不可用、Consumer 断线、Consumer 频繁重连、Outbox 发布失败、
Outbox 发布状态写回失败、DLQ 新增消息、HTTP 5xx 比例和 HTTP P95 延迟。

Grafana provisioning 和 Dashboard：

```text
deploy/grafana/provisioning/datasources/prometheus.yml
deploy/grafana/provisioning/dashboards/dashboards.yml
deploy/grafana/dashboards/go-feed-system.json
```

第一版 Dashboard 覆盖 API 状态、Consumer 连接、HTTP 请求量、P95 延迟、5xx 比例、
Consumer 重连、Outbox 发布和 DLQ。

HTTP 路由标签使用 Gin 路由模板：

```text
/api/v1/videos/:id
```

不能把真实视频 ID 放进标签，否则会产生无上限的时间序列。

业务模块只依赖窄接口，Prometheus 实现集中在 `pkg/metrics`：

```text
internal/middleware -> HTTPMetrics
internal/feed       -> ConsumerMetrics
internal/outbox     -> PublishMetrics
cmd/api             -> consumerSupervisorMetrics
```

## 5. 完整环境验证

依赖：

```text
MySQL      3308
Redis      6380
RabbitMQ   5672 / 15672
API        8080
```

验证链路：

```text
启动 Redis
-> 启动 API
-> 检查 /livez、/readyz
-> 检查 /metrics
-> 注册、登录、访问 /users/me
-> 停止 RabbitMQ
-> 确认 /livez 正常、/readyz degraded
-> 启动 RabbitMQ
-> 确认 Consumer 重连和指标恢复
```

## 6. 当前任务

已完成：

- [x] MySQL、Redis、RabbitMQ 分层健康检查。
- [x] Consumer Supervisor 与断线重连。
- [x] 独立 RabbitMQ Consumer Connector。
- [x] 真实连接故障注入恢复测试。
- [x] DLQ 查询与重放工具。
- [x] HTTP、Consumer、DLQ、Outbox Prometheus 指标。
- [x] 完整依赖环境人工验证。
- [x] `go test ./...`、`go vet ./...`、格式检查通过。
- [x] Prometheus 本地抓取配置和基础告警规则。
- [x] Grafana 数据源和 Dashboard provisioning。

待完成：

- [ ] Docker Compose 与 CI。

## 面试追问

1. 为什么 MySQL 是 critical dependency，而 Redis 和 RabbitMQ 可以降级？
2. 为什么健康检查不能直接返回底层连接错误？
3. 为什么 RabbitMQ 断线时不能让 API 启动失败？
4. Supervisor 退避为什么要设置上限，不能无限翻倍？
5. 为什么 DLQ 重放必须先发布成功再 ACK？
6. 为什么指标标签不能使用真实路径参数？
7. 为什么 `/metrics` 暂时复用业务端口，生产环境要拆开？
