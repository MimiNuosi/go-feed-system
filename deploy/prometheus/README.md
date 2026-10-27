# Prometheus 配置

本目录提供 Go Feed System 的本地 Prometheus 抓取和告警配置。

## 文件

```text
prometheus.yml  抓取 /metrics
alerts.yml      基础告警规则
```

Compose 默认分别抓取两个 API 副本：

```text
http://api-1:8080/metrics
http://api-2:8080/metrics
```

如果 Prometheus 直接运行在 Windows 主机，可以临时改为抓取 Nginx
`127.0.0.1:8080`，但这样得到的是轮询到某个 API 的指标；要区分双副本，
应保持 Compose 内的 `api-1`、`api-2` 两个 target。

```yaml
targets:
  - 127.0.0.1:8080 # 仅适合快速查看，不区分两个副本
```

## 告警

当前规则：

```text
APIDown
FeedConsumerDisconnected
FeedConsumerReconnectFlapping
OutboxPublishFailures
OutboxMarkPublishedFailures
RealtimeSubscriberDisconnected
RealtimePublishFailures
RealtimeHandlerErrors
RateLimitRedisFallback
BackgroundWorkerNoLeader
BackgroundWorkerLeaseErrors
DLQMessagesDetected
HighHTTP5xxRate
HighHTTPP95Latency
```

阈值是第一阶段保守配置，压测后应按真实 SLO 调整。
