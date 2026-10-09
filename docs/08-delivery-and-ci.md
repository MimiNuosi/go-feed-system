# 阶段 8：交付、Docker Compose 与 CI

阶段 7 解决的是运行时可观测性：

```text
健康检查
自动重连
DLQ 恢复
Prometheus 指标
Grafana Dashboard
```

阶段 8 解决的是如何稳定地把整个系统交付和复现：

```text
构建镜像
启动完整环境
挂载配置和数据卷
运行自动化检查
验证配置
```

这些内容属于交付和自动化，不是业务功能，因此单独作为阶段 8。

## 目标

最终应能够使用一条命令启动完整本地环境：

```powershell
docker compose up --build
```

启动后至少包含：

```text
api
mysql
redis
rabbitmq
prometheus
grafana
```

## 计划内容

### 8.1 Dockerfile

- [x] 为 API 编写多阶段 Dockerfile。
- [x] 使用较小运行时镜像。
- [x] 只复制运行时需要的二进制和静态资源。
- [x] 增加 `.dockerignore`，排除缓存、数据、密钥和构建产物。
- [x] 安装 Docker Desktop 后执行 `docker build` 验证镜像。
- [x] 配置 Docker Hub 镜像加速器和 Go 模块代理。
- [ ] 保留 `go test` 和 `go vet` 在构建阶段执行的可能。

### 8.2 Docker Compose

- [x] MySQL 使用持久化 volume。
- [x] Redis 使用密码并持久化数据。
- [x] RabbitMQ 开启管理插件。
- [x] API 通过环境变量读取全部配置。
- [x] Prometheus 抓取 `api:8080/metrics`。
- [x] Grafana 自动加载阶段 7 的 provisioning 和 Dashboard。
- [x] 为关键依赖配置 healthcheck。
- [x] 使用 `depends_on` 控制启动顺序，但仍由应用自行处理短暂依赖故障。
- [x] 支持通过环境变量覆盖宿主机端口，避免和 Windows 原生服务冲突。

注意：当前 `deploy/prometheus/prometheus.yml` 的目标是
`127.0.0.1:8080`，面向 Windows 本机运行。Docker Compose 中需要改为：

```text
api:8080
```

Grafana 数据源已经使用：

```text
http://prometheus:9090
```

### 8.3 配置与密钥

- [x] 提供 `.env.example`，不提交真实密码。
- [x] 保留本地 `.env`，继续加入 `.gitignore`。
- [x] 说明 JWT、MySQL、Redis、RabbitMQ 配置项。
- [x] 区分本地开发默认值和部署环境变量。

### 8.4 CI

建议使用 GitHub Actions，至少执行：

```text
gofmt 检查
go test ./...
go vet ./...
go build ./cmd/...
Prometheus YAML 解析测试
Grafana provisioning 和 Dashboard JSON 解析测试
```

CI 的作用类似自动化的 C++ 构建机：

```text
每个提交
-> 编译
-> 运行测试
-> 检查格式
-> 验证部署配置
```

当前 workflow：

```text
.github/workflows/ci.yml
```

已完成：

- [x] 检查 Go 代码格式。
- [x] 运行 `go test ./...`。
- [x] 运行 `go vet ./...`。
- [x] 构建 `./cmd/...`。
- [x] 校验 Prometheus、Grafana 和 GitHub Actions 配置。
- [ ] 后续按需增加 MySQL、Redis、RabbitMQ service container 集成任务。

### 8.5 文档

- [x] README 增加一键启动步骤。
- [x] 记录端口、账号来源和健康检查地址。
- [x] 记录 Prometheus 和 Grafana 地址。
- [ ] 记录常见故障处理方式。

## 验收标准

- [ ] 新机器只需 Docker 和 Compose 即可启动完整环境。
- [ ] MySQL、Redis、RabbitMQ 数据重启后不丢失。
- [ ] API 可在 RabbitMQ 暂时不可用时启动。
- [ ] `/livez`、`/readyz` 和 `/metrics` 可访问。
- [ ] Prometheus 能抓到 API 指标。
- [ ] Grafana 自动出现 `Go Feed System` Dashboard。
- [ ] CI 对非格式化、测试失败和无效配置返回失败。

## 面试追问

1. Docker Compose 为什么通常用于本地开发和集成测试，而不是直接等同生产部署？
2. 为什么健康检查不能只写 `depends_on`，应用仍要做重连和降级？
3. 为什么不能把 `.env` 中的真实密码提交到仓库？
4. 多阶段 Dockerfile 相比单阶段构建有什么好处？
5. CI 为什么必须验证测试之外的内容，例如格式和部署配置？
6. Docker Compose 内部服务为什么使用 `mysql:3306`，而不是宿主机端口？
