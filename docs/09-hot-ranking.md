# 阶段 9：热门 Feed、滑动窗口热榜与防击穿

阶段 8 已经让系统可以通过 Docker Compose 完整启动。阶段 9 开始补短视频产品
最核心的业务能力之一：

```text
浏览事件
-> 浏览量统计
-> 滑动窗口热度计算
-> 热门 Feed
-> 缓存防击穿
```

## 已确定的设计

时间窗口：

```text
最近 1 小时
最近 24 小时
```

热度评分：

```text
like * 3 + comment * 5 + view * 1
```

浏览量：

```text
Redis 负责实时累加
后台任务批量回写 MySQL
```

## 目标接口

记录浏览：

```http
POST /api/v1/videos/:id/view
Authorization: Bearer <jwt> 可选
```

热门 Feed：

```http
GET /api/v1/feed/hot?window=1h&cursor=<opaque>&page_size=20
```

`window` 第一版支持：

```text
1h
24h
```

响应格式尽量复用关注 Feed 的 Item 结构，避免前端处理两套视频 DTO。

## Redis Key 设计

浏览去重：

```text
view:dedup:<videoID>:<viewerKey>
```

建议 TTL：

```text
5 分钟
```

`viewerKey` 登录用户使用 user ID，匿名用户使用客户端 IP 哈希。

注意匿名这一半的实际强度有限，原因有两层：

```text
1. 路由层已经关闭 X-Forwarded-For 采信（见“安全边界”一节），
   客户端无法再伪造 IP 换身份。
2. 但关闭之后，同一个部署里所有匿名请求的对端地址都一样，
   于是匿名身份退化成“整个部署共用一个身份”：
   匿名浏览量对每个视频最多只能加 1 分。
```

也就是说匿名去重目前是“防刷分够用、统计意义不足”。要让匿名浏览重新成为
有意义的信号，必须引入客户端设备 ID，而设备 ID 是客户端可控的，需要限流
兜底才敢采信，见 9.6。

待回写浏览量：

```text
video:views:pending
```

使用 Redis Hash：

```text
field: videoID
value: 待写回 MySQL 的增量
```

滑动窗口热榜：

```text
hot:video:1h:<bucket>
hot:video:24h:<bucket>
```

每个 bucket 使用 Redis ZSET：

```text
member: videoID
score:  该 bucket 内的加权热度
```

查询时合并最近 N 个 bucket：

```text
1h  -> 最近的 1 分钟 bucket
24h -> 最近的 5 分钟 bucket
```

## 热度更新流程

### 安全边界：为什么必须关闭 XFF 采信

`X-Forwarded-For` 是客户端可以随意伪造的头部，而 gin 默认信任全部代理，
等于让客户端自己申报 IP。当前部署下这条路径是真实可利用的：

```text
客户端经 Docker 端口映射进入容器
-> 容器看到的对端固定是网桥网关 172.18.0.1
-> 172.18.0.1 落在常见“可信私有网段”里
-> 若按私有网段配置可信列表，客户端自带的 XFF 就会被采信
-> 每换一个伪造值就是一个“新观众”，可以无限刷热榜
```

因此路由层显式执行 `engine.SetTrustedProxies(nil)`，`ClientIP()` 直接返回
对端地址。等以后在前面部署受控网关（并且 api 不再直接暴露端口）时，再把
网关地址填进可信列表，并确认网关是覆盖而不是追加 XFF。

```text
用户浏览视频
-> 检查 view:dedup
-> 未重复则 HINCRBY video:views:pending
-> 对 1h 和 24h bucket 执行 ZINCRBY
-> 返回 204
```

点赞、评论也会产生热度：

```text
点赞   +3
评论   +5
取消点赞 -3
删除评论 -5
```

负分不能随意把总热度减成负数，查询和服务端需要明确边界行为。

### 游标分页的骨架（阶段 9.2 补充）

已经交付的部分：

```text
internal/hot/cursor.go
  EncodeCursor / DecodeCursor   不透明游标 base64(JSON)，内容只有 (score, video_id)
internal/hot/service.go
  ListHot 返回 *Page            Videos + NextCursor + HasMore
  按 pageSize+1 取数            多出来的一条只用于判断 HasMore
  buildPage                     裁剪到 pageSize 并生成 next_cursor
internal/hot/handler.go
  cursor 参数解析               非法游标返回 400 INVALID_ARGUMENT
  透传 HasMore / NextCursor     写进 feedmodel.Page
internal/hot/redis_repository.go
  rangeFromCursor               分两段读：同分接续 + 更低分数补齐
```

真实链路实测（4 个视频都是 1 分，构成一个 4 成员的同分带）：

```text
ZSET 顺序:  4(1) 3(1) 2(1) 1(1)
第一页 page_size=2 -> ids=4,3   has_more=true
  next_cursor=eyJzY29yZSI6MSwidmlkZW9faWQiOjN9   （即 score=1, video_id=3）
第二页 -> ids=2,1               has_more=false   （边界项 3 没有重复出现）
两页合并 = 4,3,2,1，无重复、无遗漏
非法游标 -> 400 {"code":"INVALID_ARGUMENT","message":"invalid cursor"}
```

为什么游标必须同时带上 videoID，而不是只带分数：

```text
热榜整体顺序是 (score DESC, videoID DESC)。
如果翻页条件只写 "score < cursor.Score"，和游标同分的其余成员会被整段跳过：
例如 102 和 103 都是 20 分，第一页在 103 处截断，第二页就再也读不到 102。
```

所以后续页要分两段读：

```text
第 1 段：score 等于游标分数，且 member 字典序小于游标 member 的那些
第 2 段：score 严格小于游标分数的所有成员（第 1 段不够 limit 时才补）
```

验收测试（internal/hot/redis_repository_test.go）专门构造了同分数据：

```text
种子：101(30)  103(20)  102(20)  104(10)
第一页 limit=2 -> [101, 103]
第二页 limit=2 -> [102, 104]      <- 同分接续，最容易漏
第三页 limit=2 -> []
另外还逐条翻页一遍，断言整体顺序是 [101, 103, 102, 104] 且不重不漏
```

跑法（本机 Redis 不可用时会自动跳过）：

```text
TEST_REDIS_ADDR=<addr> TEST_REDIS_DB=15 TEST_REDIS_PASSWORD=<pass> \
  go test -run TopVideoIDsPaginationIntegration ./internal/hot/
```

已知的优化点（不影响正确性）：第 1 段用的是
`ZRangeByScoreWithScores(Min=score, Max=score)`，会把同一个分数上的
成员整段读回内存。如果某个分数上挂了大量视频（长尾数据里很常见），
每次翻页都要多读一大段。更省的做法是先用 `ZRANK` 拿到游标 member 的排名 r，
再用 `ZRANGE(key, max(0, r-limit), r-1)` 只取紧邻游标的那 limit 条，
复杂度从 O(同分带大小) 降到 O(log n + limit)。

## 热门 Feed 查询

```text
根据 window 计算需要合并的 bucket
-> ZUNIONSTORE 到临时 ZSET
-> 按 score DESC 排序
-> 同分使用 videoID DESC 保持稳定
-> 过滤不可见或已删除视频
-> 批量补充作者和互动信息
-> 返回 Feed Item
```

热门 Feed 是跨领域读模型，可以放在：

```text
internal/hot
```

不要让 `video.Repository` 或 `interaction.Repository` 负责排行榜查询。

## 防击穿与热点保护

至少需要：

```text
singleflight
本地缓存
Redis stale cache
TTL jitter
```

流程：

```text
本地缓存命中 -> 直接返回
本地缓存未命中 -> singleflight 合并并发请求
Redis 热榜命中 -> 更新本地缓存
Redis 热榜未命中 -> 从 bucket 重建
Redis 故障 -> 使用 stale cache
```

本地缓存 TTL 第一版建议：

```text
5 秒到 10 秒
```

不能用无限 TTL，否则热点数据长期不更新。

## 浏览量回写

新增后台任务：

```text
定期读取 video:views:pending
-> 批量 UPDATE videos.view_count
-> 成功后删除对应 Hash 增量
```

必须使用 Lua 或“读取后再删除”的安全策略，避免增量丢失。

## 数据库迁移

按照项目约束，以下 migration 由学习者自己创建和执行：

```text
007_add_video_view_count.sql
```

建议增加：

```text
videos.view_count BIGINT UNSIGNED NOT NULL DEFAULT 0
```

热度查询的候选集来自 Redis，因此第一阶段不需要在 MySQL 维护热榜排名。

## 当前任务

### 已交付的实现细节

计划之外、实现时确定下来的几个决定：

```text
热度写入：一次 Lua 同时 ZINCRBY 到 1h 和 24h 两个 bucket，保证原子
排序稳定：bucket member 使用 20 位补零 videoID，
          ZSET 在分数相同时按字典序倒序，等价于“同分时 videoID 大的在前”
热度记录：点赞/评论通过 best-effort 的 ScoreRecorder 写热度，
          热榜更新失败只记日志，不影响点赞评论本身成功
去重与 ack：浏览去重和增量累加放在同一个 Lua 里，避免重复计数
共享 DTO：热门 Feed 与关注 Feed 共用 pkg/feedmodel，
          避免前端处理两套 Item 结构
```

### 9.1 浏览事件

- [x] 创建并执行 `007_add_video_view_count.sql`。
- [x] 实现浏览去重（`view:dedup:<videoID>:<viewerKey>`，TTL 5 分钟）。
- [x] 实现 Redis 浏览量增量（`video:views:pending` Hash，写入与去重同一个 Lua）。
- [x] 实现 `POST /api/v1/videos/:id/view`（重复浏览同样返回 204，保证幂等）。
- [x] 补充 Handler、Service 和 Redis 测试。

### 9.2 滑动窗口热榜

- [x] 实现时间 bucket 计算（1h 用 1 分钟桶，24h 用 5 分钟桶）。
- [x] 实现浏览、点赞、评论热度加权（view +1、like +3、comment +5，取消与删除写负分）。
- [x] 实现 bucket 合并查询（`ZUNIONSTORE` 到临时 ZSET）。
- [x] 处理同分排序（20 位补零 member，等价于同分时 videoID 大的在前）。
- [x] 处理游标分页（游标编解码、pageSize+1 判 has_more、同分接续读取）。
- [x] 过滤不可见视频（`ListReadyByIDs`，查不到的 ID 直接跳过）。

### 9.3 热门 Feed

- [x] 实现 `GET /api/v1/feed/hot`。
- [x] 复用作者和互动信息批量补充（视频、作者、点赞数各一次批量查询）。
- [x] 与关注 Feed 共用排序和 Item DTO（`pkg/feedmodel`）。
- [x] 补充 HTTP 集成测试（真实 Compose 环境端到端验证）。

### 9.4 防击穿

- [x] 使用 `singleflight` 合并并发缓存未命中。
- [x] 增加本地热点缓存（fresh 5 秒 / stale 30 秒）。
- [x] 增加 stale cache 降级（Redis 故障时返回 stale 数据并记 Warn 日志）。
- [ ] 增加 TTL jitter。
      未做：jitter 解决的是多实例在同一瞬间齐刷刷过期，
      当前单实例部署 + singleflight 已经合并了击穿，留到多副本时再补。
- [x] 补充并发和 Redis 故障测试。

### 9.5 浏览量回写

- [x] 回写 Worker、指标与 main 接线。
- [x] 实现 Redis 侧 `PendingViews` / `AckPendingView`。
- [x] 实现 MySQL 侧 `video.GORMRepository.AddViewCount`。
- [x] 增加失败重试和指标（重试靠"不 ack"实现，指标为 `view_writeback_total`）。

已交付的文件与职责：

```text
internal/hot/writeback.go
  PendingViewStore      Redis 侧：读增量 + ack
  ViewCountWriter       MySQL 侧：累加 view_count
  ViewWriteback         单轮逻辑：取增量 -> 写库 -> ack
  ViewWritebackWorker   ticker 调度，默认 5 秒
pkg/metrics
  view_writeback_total{result="success|write_error|ack_error"}
cmd/api/main.go
  构造 worker，并在 run() 里和 HTTP server、Feed messaging 并行运行
internal/hot/redis_repository_test.go
  TestRedisRepository_PendingViewsIntegration 就是验收测试
```

三个存取方法的落地方式：

```text
1. PendingViews      HGetAll 全量读回，解析 field/value，
                     脏数据跳过并记 Warn，最后按 limit 截断
2. AckPendingView    HDel，field 用普通十进制 videoID（必须和 Lua 里 HINCRBY 一致）
3. AddViewCount      一条 UPDATE 做原子自增 + WHERE id，
                     RowsAffected == 0 按成功处理
```

验证方式：本机 Redis 可用时跑验收测试（测试覆盖脏数据跳过、
limit 截断、重复 ack 幂等）：

```text
TEST_REDIS_ADDR=<addr> TEST_REDIS_DB=15 TEST_REDIS_PASSWORD=<pass> \
  go test -run PendingViewsIntegration ./internal/hot/
```

播放量的读取路径已经打通：`VideoDetail` 和 Feed 的 `Item` 都带上了
`view_count`，值来自 MySQL 快照（`SELECT *` 自动带出，仓储层无需额外查询）。

实测到的滞后现象（这就是"最终一致"的真实样子）：

```text
发一次浏览 -> 热榜 Item 立刻能查到，view_count 还是 1
-> 7 秒后 MySQL 变成 2，详情接口变成 2
```

待办：详情接口的实时补偿。现在是只读 MySQL，最多滞后一个回写周期
（默认 5 秒）。要做到"自己刚看完就显示最新数字"，需要在读路径上合并
Redis 里还没回写的增量（`HGET video:views:pending <videoID>`），
显示 `mysql + pending`。代价是每个详情请求多一次 Redis 读，
并且要定义 Redis 不可用时的降级行为（例如降级为只读 MySQL）。

`RedisRepository` 的 logger 已改成构造参数注入（`NewRedisRepository(client, logger)`），
不再使用 `slog.Default()`，保证 Worker 的日志和应用日志走同一个 handler。

已确定的做法（方案 A，宁可多算不可少算）：

```text
每轮 HGETALL 取一批 videoID 和增量
-> 逐条 UPDATE videos SET view_count = view_count + ?
-> 该条成功后才 HDEL 对应的 Hash 字段（这一步就是 ack）
-> 失败的条目不删，下一轮 Redis 里自然还有，等于自动重试
```

这里刻意不引入“失败项清单”：

```text
Redis Hash 本身就是待处理队列，删除动作就是 ack。
失败时什么都不做，下一轮重新读到，因此不需要额外的状态表。
代价是 HDEL 失败、或进程在“写库成功、删增量之前”崩溃时，
下一轮会重复累加一次。浏览量允许这种误差。
```

如果以后要求绝不重复计数（比如变成计费口径），就要真正引入幂等：
给每批增量一个唯一 ID 落库（`INSERT IGNORE` 去重）或者改为写绝对值，
而不是指望“整批重试”——`view_count = view_count + delta` 本身不幂等。

### 9.6 限流与匿名身份（新增）

背景：XFF 采信关闭之后，热榜刷分这条路已经被堵住（匿名请求最多给一个视频
加 1 分），限流的价值转为两件事：

```text
1. 可用性保护：挡住单机大量请求打满 API、MySQL 和 Redis。
2. 为匿名身份升级兜底：要让匿名浏览重新可统计，就要采信客户端设备 ID，
   而设备 ID 可伪造，必须有限流限制单个来源的尝试次数。
```

计划：

- [x] 选择令牌桶实现位置：**本地进程内存**。
- [x] 在鉴权之前加限流，超限返回 429（带 `Retry-After` 和 `RATE_LIMITED`）。
- [x] 增加被限流次数的指标 `http_rate_limited_total{scope}`。
- [x] 令牌桶核心算法（假时钟驱动的验收测试全绿）。
- [ ] 设计匿名设备 ID 的来源与降级链（设备 ID 优先，IP 兜底）。
- [ ] 更严格的按路由额度（例如登录失败锁定）。

### 为什么额度放本地而不是 Redis

```text
1. 只有一个 api 实例，本地额度就是精确额度；
   放 Redis 会把精确性换成每请求一次网络往返，而当前并不需要。
2. 保护系统的东西不应该依赖系统里最脆弱的组件。
   限流器最该发挥作用的时刻正是流量峰值，那时它在给 Redis 加压。
   而且 Redis 故障时没有好语义：fail-open 等于没限流，fail-closed 等于全站 503。
3. 边界清晰：水平扩展后每个实例各自持有完整额度，
   集群总额度会变成 N 倍，那时才需要 Redis + Lua 做全局额度，
   或者按实例数分摊。
```

### 两层的维度设计

```text
global   所有请求共用一个桶：长期平均 1000 rps，突发 2000
identity 登录用户按 userID 各自一个桶：长期平均 4 rps，突发 480
```

identity 这一层对匿名请求直接跳过，原因是按 IP 拿不到可信来源：
关掉 XFF 采信之后所有匿名请求的对端地址都是同一个网桥地址，
按 IP 限流会退化成"全站共用一个桶"，一个人就能把所有人的额度刷掉。
匿名流量目前只由 global 桶兜底，等有了设备 ID 再补降级链。

### 两个必须处理的细节

```text
1. 探针豁免：/livez、/readyz、/metrics 不参与限流。
   否则额度打满时健康检查失败，容器被判不健康重启，反而制造雪崩。
2. 桶必须回收：per-key 限流器如果只增不减，
   攻击者不停更换 key 就能把内存撑爆。
   实现里用"最后访问时间 + 限频清扫"回收空闲桶，
   并且把清扫频率限制成每分钟最多一次，避免 O(n) 扫描退化成 O(n^2)。
```

已交付的文件：

```text
pkg/ratelimit/limiter.go       令牌桶 + 按 key 管理 + 空闲回收
pkg/ratelimit/limiter_test.go  验收测试（假时钟驱动，覆盖补充/爆桶/回收/并发）
internal/middleware/ratelimit.go      限流中间件 + 两种限流键
internal/middleware/ratelimit_test.go
internal/router/router.go             挂载位置：Recovery 之后、路由之前
cmd/api/main.go                       额度初值（刻意给得很宽）
```

限流的实测结果（identity 层：突发 480、补充 4/秒）：

```text
连打 600 次已登录接口，耗时 1.33 秒
HTTP 200 -> 485 次      （480 突发 + 1.33s * 4/s ≈ 5）
HTTP 429 -> 115 次      Retry-After: 1，body code = RATE_LIMITED
同时 /livez 和 /readyz 仍是 200（探针豁免生效）
http_rate_limited_total{scope="identity"} 115
```

一个踩过的坑：身份限流的键来自 `authctx.UserID`，而它是鉴权中间件写进
context 的。所以身份限流不能像全局限流那样挂在最外层 ——
挂在鉴权之前时 context 里还没有 userID，限流器只会看到空 key 然后跳过，
那一层就等于没生效。现在它挂在鉴权之后（auth 组内 + 三个可选鉴权路由上），
并且有 `TestNew_IdentityRateLimitSeesAuthenticatedUser` 卡住这个顺序。

## 面试追问

1. 为什么浏览计数不能每次直接 `UPDATE videos`？
2. 为什么滑动窗口比固定窗口更适合热榜？
3. 为什么热榜要用 Redis ZSET，而不是普通 Set？
4. `ZUNIONSTORE` 合并大量 bucket 时有什么性能风险？
5. 为什么取消点赞和删除评论会改变热榜分数？
6. singleflight 解决什么问题，又不能解决什么？
7. 本地缓存为什么必须设置短 TTL？
8. Redis 热榜丢失后如何从 MySQL 和互动数据恢复？
9. 浏览量回写为什么选“先写库再删增量”，而不是“先删增量再写库”？
10. `view_count = view_count + delta` 幂等吗？如果要求不重复计数应该怎么改？
11. 关掉 XFF 采信之后，匿名去重变成了什么语义？这样做的代价是什么？
