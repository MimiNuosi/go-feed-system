# Grafana 配置

本目录通过 provisioning 自动加载 Prometheus 数据源和 Go Feed System Dashboard。

## 文件

```text
provisioning/datasources/prometheus.yml  Prometheus 数据源
provisioning/dashboards/dashboards.yml   Dashboard 自动加载配置
dashboards/go-feed-system.json           第一阶段 Dashboard
```

默认数据源地址：

```text
http://prometheus:9090
```

该地址面向未来 Docker Compose 环境。如果 Prometheus 安装在 Windows 本机，
需要把地址改为：

```text
http://127.0.0.1:9090
```

## Dashboard 面板

```text
API Up
Consumer Connection
HTTP Request Rate
HTTP P95 Latency
HTTP 5xx Rate
Consumer Reconnect Events
Outbox Publish Events
DLQ Messages
```

Dashboard 通过 JSON 文件纳入版本控制，不需要在 Grafana 页面手工导入。
