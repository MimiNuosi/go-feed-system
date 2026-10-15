# Go Feed System

基于 Go 的短视频 Feed 流后端，从零实现账号、视频上传、点赞/评论/关注、
Feed 流与热榜，并把异步解耦、缓存一致性、限流与可观测性做到可交付的程度。

三个最值得看的点：

```text
1. Feed 推拉结合 + 复合游标分页：普通作者写扩散进粉丝收件箱（Redis ZSet，
   score 用发布时间），粉丝过阈值的大 V 跳过写扩散、改在读时从 MySQL 拉取，
   再与收件箱结果合并去重；游标带 (created_at, id) 与 (score, videoID)，
   同分/同毫秒边界不重不漏
2. 滑动窗口热榜 + 两级缓存：1h/24h 双 bucket 用单次 Lua 原子更新，
   排名结果用 singleflight 与本地 fresh/stale 缓存抗击穿，读模型再加
   1 秒 TTL 缓存（访问者维度的 is_liked_by 拆出去实时查）；匿名请求的
   SQL 由 3 条降到 0 条，4000 rps 下 P95 1.12ms 且不随压力变化
3. 性能问题有据可查：用语句级测量发现详情接口每请求实际发出 12 条 SQL，
   优化到 3 条，同速率 P95 由 17.9ms 降至 2.4~6.0ms，容量拐点 2500 → 5000 rps
```

## 架构与请求链路

```text
HTTP 层    Gin：RequestID → AccessLog → Metrics → Recovery → RateLimit → 路由
业务层     Handler（参数校验、DTO）→ Service（业务规则、聚合）
数据层     Repository（GORM / Redis / 对象存储），接口定义在使用方
异步层     Outbox 表 → Publisher → RabbitMQ → Consumer（含重试与死信）
后台任务   浏览量回写 Worker、Feed Inbox 重建 CLI、DLQ 查询与重放 CLI
```

一次典型的写请求（发布视频）：

```text
POST /api/v1/videos
-> 校验内容嗅探（不信任客户端声明的类型）
-> 落盘对象存储 + 同事务写 videos 与 outbox_events
-> 返回 201；后台 Publisher 异步投递事件，Consumer 写扩散到粉丝收件箱
```

## 技术选型与理由

| 组件 | 用它解决什么 | 取舍 |
| --- | --- | --- |
| Gin | HTTP 路由与中间件链 | 比原生 net/http 少样板，中间件顺序显式可控 |
| GORM | CRUD 与事务 | 团队效率优先；热点查询用 `SELECT` 明确列 + 索引兜底 |
| MySQL | 元数据与关系数据 | 强一致、事务边界清晰；不做缓存替代品 |
| Redis | 缓存、排行榜、去重、增量 | ZSET 天然适配滑动窗口；Lua 保证多 key 原子 |
| RabbitMQ | 事件驱动解耦 | 相比进程内队列可持久化、可重试、可进死信 |
| Outbox 表 | 业务写入与消息投递的一致性 | 换取最终一致，代价是至少一次投递 + 幂等消费 |
| 进程内令牌桶 | 限流 | 单实例下额度精确、零网络开销；多实例需换 Redis |
| Prometheus/Grafana | 指标与面板 | 指标名和标签基数受控，避免高基数标签 |

## 关键设计

- **推拉结合**：按粉丝数阈值（默认 1000）分流 —— 普通作者发布时写扩散到粉丝收件箱，大 V 跳过写扩散、改在读时按作者拉取，避免百万粉丝被逐一写入。见 [docs/06](docs/06-feed-scaling.md)、`internal/feed/fanout.go`
- **复合游标分页**：不用 offset（并发插入会跳页），游标同时带排序键与主键，处理同分/同毫秒边界。见 [docs/09](docs/09-hot-ranking.md)、`internal/hot/cursor.go`
- **滑动窗口热榜**：1h 用 1 分钟桶、24h 用 5 分钟桶，查询时合并；member 用定长补零 ID 让同分稳定排序
- **防击穿**：singleflight 合并并发未命中 + 本地 fresh(5s)/stale(30s) 两级缓存，Redis 故障时降级返回 stale 数据
- **读模型缓存**：热榜每页要 3 条 SQL 补齐视频、作者、点赞数，这部分与访问者无关，用 1 秒 TTL 缓存；`is_liked_by` 属于访问者维度，永远实时查、绝不进缓存，所以同一条缓存可以被所有匿名用户共享
- **发现路径**：`/feed/following` 只给关注关系、`/feed/hot` 只给有热度的内容，两者都覆盖不到"刚发布、没人关注"的视频；`/feed/latest` 补上这条冷启动入口
- **至少一次 + 幂等**：去重键 + 唯一索引兜住重复消费；浏览量回写采用"先写库、后删增量"，失败条目留在 Hash 中自动重试
- **限流粒度**：全局桶保护整体吞吐 + 按身份桶，探针与 /metrics 豁免，避免"额度打满导致健康检查失败、容器反复重启"
- **分级探针**：`/livez` 只看进程存活，`/readyz` 区分关键依赖（MySQL）与可降级依赖（Redis、RabbitMQ）

## 性能

单机 Docker（限 6 核 / 6GB）压测，客户端与服务同机，开环固定速率，
数据量极小（个位数视频）。热榜那一组是**受控 A/B** 的结果
（同一台机器、同一份数据、连续两轮）—— 跨会话的数字不可比，这一点我踩过坑。

```text
接口 / 配置                       速率       mean       P95       说明
GET /livez（探针）                4000 rps   540µs      610µs     天花板参考
GET /api/v1/feed/hot（缓存关闭）   4000 rps   1.81ms     3.60ms    每请求 3.01 条 SQL
GET /api/v1/feed/hot（缓存开启）   4000 rps   0.65ms     1.12ms    每请求 0.01 条 SQL
GET /api/v1/videos/1（优化前）     4000 rps   6.27ms     17.9ms    每请求 12.04 条 SQL
GET /api/v1/videos/1（优化后）     4000 rps   1.4~2.1ms  2.4~6.0ms 每请求 3.02 条 SQL
```

两处优化的资源侧变化（2000 rps 稳态）：

```text
                       SQL/请求     api CPU    mysql CPU
热榜 缓存关闭/开启      3.01/0.01    150%/43%   75%/0.5%
详情 优化前/后          12.04/3.02   196%/181%  200%/125%
```

完整的测量方法、受控 A/B、瓶颈定位，以及踩过的三个坑（空榜单假象、
只报最好的一次、环境噪声导致误判）见 [docs/10](docs/10-load-testing.md)，
脚本在 [`deploy/loadtest/`](deploy/loadtest/README.md)。

## 已知局限与后续规划

```text
1. 匿名浏览去重按"对端地址"退化成了全站单一身份，需要客户端设备 ID + 限流兜底
2. 详情接口的浏览量只读 MySQL 快照，最多滞后一个回写周期（默认 5 秒）
3. 限流是进程内的，水平扩展后集群总额度会变成实例数的倍数
4. 热榜第 1 段读取会把同一分数的成员整段读回，长尾数据下应改为 ZRANK + 限量 ZRANGE
5. 视频只做容器级校验（ftyp/EBML），没有转码与编码级校验
6. 未做真正的多实例部署验证（负载均衡、会话一致性）
7. 热榜读模型缓存有 1 秒陈旧窗口：热门视频的点赞数最多滞后 1 秒才更新
8. 内嵌控制台是单文件内联脚本、没有 CSP，只用于人工验证；生产环境应交给
   nginx/CDN 与独立前端工程
```

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
vegeta（压测）
go:embed（内嵌控制台，免 npm 构建）
```

## 当前阶段

阶段 12：演示手册与交付收尾（已完成）。

已完成：

```text
阶段 0-4   服务骨架、HTTP 分层、鉴权、视频上传、互动
阶段 5-6   Feed 主链路、写扩散与推拉结合
阶段 7     健康检查、消费端重连、DLQ、Prometheus 指标
阶段 8     Docker Compose 完整环境、多阶段构建、GitHub Actions
阶段 9     滑动窗口热榜、防击穿、浏览量回写、两层限流、游标分页
阶段 10    vegeta 速率阶梯压测、语句级瓶颈定位与两步优化
阶段 11    go:embed 内嵌控制台、最新频道与按作者作品页、播放量自动计数
阶段 12    端到端走查脚本、演示手册、文档与验收项对齐代码
```

## 项目结构

```text
cmd/api              HTTP API 服务
cmd/dlqctl           DLQ 查询与重放 CLI
cmd/feedrebuild      Feed Inbox 重建 CLI

internal/            用户、视频、互动、Feed 等业务模块
internal/web         内嵌的最小控制台页面（go:embed 打包进二进制）
pkg/                 配置、日志、数据库、Redis、RabbitMQ、指标等基础能力
migrations/          只读 SQL 迁移
deploy/prometheus    Prometheus 配置与告警
deploy/grafana       Grafana 数据源和 Dashboard
deploy/loadtest      vegeta 压测脚本与目标文件
deploy/demo          端到端走查脚本（注册/上传/互动/Feed 全链路冒烟）
docs/                分阶段设计记录与压测报告
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

浏览器打开 `http://127.0.0.1:<API_HOST_PORT>/` 就能看到内置的最小控制台
（注册登录、上传视频、翻最新 / 关注 Feed / 热榜 / 我的视频、进详情页播放/点赞/评论/关注）。
它由 API 同源提供（`internal/web`，用 `go:embed` 打进二进制），
用来人工验证整条链路是否正常，见 [docs/11](docs/11-minimal-console.md)。

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

端到端走查（会自动注册两个账号，把上传到 Feed 的全部接口跑一遍）：

```powershell
pwsh -File deploy/demo/walkthrough.ps1 -SampleVideo D:\path\to\clip.mp4
```

它把 [docs/12](docs/12-demo-runbook.md) 里的演示路线固化成脚本，
失败时退出码为 1，可以当冒烟测试用。

## 常用开发命令

```powershell
$env:GOCACHE = "D:\go_feedSystem\.cache\go-build"

gofmt -w cmd internal pkg deploy
go test ./...
go vet ./...
go build ./cmd/...
```

不带环境变量时 `go test ./...` 只跑单元测试，集成测试会自行跳过。
要跑集成测试（需要上面那套 Compose 起着），按下面给变量赋值：

```powershell
$env:TEST_MYSQL_DSN='feed_app:<MYSQL_PASSWORD>@tcp(127.0.0.1:13308)/go_feed?charset=utf8mb4&parseTime=True&loc=Local&interpolateParams=true'
$env:TEST_REDIS_ADDR='127.0.0.1:16380'
$env:TEST_REDIS_DB='15'
$env:TEST_REDIS_PASSWORD='<REDIS_PASSWORD>'
$env:TEST_RABBITMQ_URL='amqp://feed_app:<RABBITMQ_DEFAULT_PASS>@127.0.0.1:15673/'

go test -count=1 ./...
```

`interpolateParams=true` 必须带上：Docker 里的 API 就是这么连的，
少了它测出来的是驱动的另一套行为（阶段 11 踩过这个坑，见
[docs/11](docs/11-minimal-console.md)）。集成测试用 `TEST_REDIS_DB=15`
隔离，不会碰业务库的业务 Redis 数据。

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
- [阶段 9：热门 Feed、滑动窗口热榜与防击穿](docs/09-hot-ranking.md)
- [阶段 10：压测与性能调优](docs/10-load-testing.md)
- [阶段 11：最小前端控制台（人工验证）](docs/11-minimal-console.md)
- [阶段 12：演示手册（5 分钟走完全链路）](docs/12-demo-runbook.md)
