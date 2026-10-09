# Prometheus 配置

本目录提供 Go Feed System 的本地 Prometheus 抓取和告警配置。

## 文件

```text
prometheus.yml  抓取 /metrics
alerts.yml      基础告警规则
```

Compose 默认抓取地址：

```text
http://api:8080/metrics
```

如果 Prometheus 直接运行在 Windows 主机，需要把 `prometheus.yml` 中的
target 改为：

```yaml
targets:
  - 127.0.0.1:8080
```

## 告警

当前规则：

```text
APIDown
FeedConsumerDisconnected
FeedConsumerReconnectFlapping
OutboxPublishFailures
OutboxMarkPublishedFailures
DLQMessagesDetected
HighHTTP5xxRate
HighHTTPP95Latency
```

阈值是第一阶段保守配置，压测后应按真实 SLO 调整。
