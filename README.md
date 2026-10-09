# Go Feed System

一个用于学习 Go 后端工程的短视频 Feed 流项目，覆盖账号、视频、关注、点赞、评论、
Feed 推拉结合、Outbox、RabbitMQ、DLQ、可观测性和 Docker Compose。

## 技术栈

```text
Go
Gin
GORM
MySQL
Redis
RabbitMQ
Prometheus
Grafana
Docker Compose
```

## 当前阶段

阶段 8：交付、Docker Compose 与 CI。

已完成：

```text
API 多阶段 Dockerfile
Docker Compose 完整环境
MySQL 持久化与迁移初始化
Redis 密码与持久化
RabbitMQ 管理与健康检查
Prometheus 抓取和告警规则
Grafana provisioning 与 Dashboard
DLQ 查询和重放工具
Feed Inbox 重建工具
```

## 项目结构

```text
cmd/api              HTTP API 服务
cmd/dlqctl           DLQ 查询与重放 CLI
cmd/feedrebuild      Feed Inbox 重建 CLI

internal/            用户、视频、互动、Feed 等业务模块
pkg/                 配置、日志、数据库、Redis、RabbitMQ、指标等基础能力
migrations/          只读 SQL 迁移
deploy/prometheus    Prometheus 配置与告警
deploy/grafana       Grafana 数据源和 Dashboard
docs/                分阶段学习记录
```

## Docker Compose 启动

### 1. 准备环境变量

```powershell
Copy-Item .env.example .env
```

根据本地环境修改 `.env`。真实密码只保存在 `.env`，不要提交到 Git。

Compose 需要重点检查：

```text
MYSQL_ROOT_PASSWORD
MYSQL_DATABASE
MYSQL_USER
MYSQL_PASSWORD
REDIS_PASSWORD
RABBITMQ_DEFAULT_USER
RABBITMQ_DEFAULT_PASS
JWT_SECRET
GRAFANA_ADMIN_USER
GRAFANA_ADMIN_PASSWORD
```

### 2. 启动完整环境

```powershell
docker compose up -d --build
```

默认端口：

```text
API         8080
MySQL       3308
Redis       6380
RabbitMQ    5672
RabbitMQ UI 15672
Prometheus  9090
Grafana     3000
```

如果宿主机端口已被占用，可以临时覆盖：

```powershell
$env:API_HOST_PORT = "18080"
$env:MYSQL_HOST_PORT = "13308"
$env:REDIS_HOST_PORT = "16380"
$env:RABBITMQ_HOST_PORT = "15673"
$env:RABBITMQ_MANAGEMENT_HOST_PORT = "15674"
$env:PROMETHEUS_HOST_PORT = "19090"
$env:GRAFANA_HOST_PORT = "13000"

docker compose up -d --build
```

### 3. 检查服务

```powershell
Invoke-RestMethod http://127.0.0.1:8080/livez
Invoke-RestMethod http://127.0.0.1:8080/readyz
curl.exe http://127.0.0.1:8080/metrics
```

Grafana 默认地址：

```text
http://127.0.0.1:3000
```

Prometheus 地址：

```text
http://127.0.0.1:9090
```

RabbitMQ 管理页面：

```text
http://127.0.0.1:15672
```

### 4. 停止

保留数据库和 Redis 数据：

```powershell
docker compose down
```

删除容器、网络和全部 volume：

```powershell
docker compose down -v
```

`down -v` 会删除本地数据库数据，只在确认不需要数据时执行。

## 运维命令

查看 DLQ：

```powershell
go run ./cmd/dlqctl list
```

预览重放：

```powershell
go run ./cmd/dlqctl replay -message-id <id>
```

执行重放：

```powershell
go run ./cmd/dlqctl replay -message-id <id> -execute
```

预览 Feed Inbox 重建：

```powershell
go run ./cmd/feedrebuild -user-id <id>
```

执行重建：

```powershell
go run ./cmd/feedrebuild -user-id <id> -execute
```

## 常用开发命令

```powershell
$env:GOCACHE = "D:\go_feedSystem\.cache\go-build"

gofmt -w cmd internal pkg deploy
go test ./...
go vet ./...
go build ./cmd/...
```

## 分阶段文档

- [阶段 0：服务启动与退出](docs/00-walking-skeleton.md)
- [阶段 1：Gin 和中间件](docs/01-http-layering.md)
- [阶段 2：用户注册、登录与 JWT](docs/02-user-auth.md)
- [阶段 3：视频上传与元数据](docs/03-video.md)
- [阶段 4：用户互动](docs/04-interaction.md)
- [阶段 5：Feed 主链路](docs/05-feed.md)
- [阶段 6：Feed 写扩散与推拉结合](docs/06-feed-scaling.md)
- [阶段 7：健康检查、重连、DLQ 与指标](docs/07-operations-observability.md)
- [阶段 8：交付、Docker Compose 与 CI](docs/08-delivery-and-ci.md)
