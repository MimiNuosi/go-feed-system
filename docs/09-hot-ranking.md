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

`viewerKey` 登录用户使用 user ID，匿名用户使用 IP 哈希。第一阶段可以只做基础
去重，后续再引入更严格的风控规则。

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

### 9.1 浏览事件

- [ ] 创建并执行 `007_add_video_view_count.sql`。
- [ ] 实现浏览去重。
- [ ] 实现 Redis 浏览量增量。
- [ ] 实现 `POST /api/v1/videos/:id/view`。
- [ ] 补充 Handler、Service 和 Redis 测试。

### 9.2 滑动窗口热榜

- [ ] 实现时间 bucket 计算。
- [ ] 实现浏览、点赞、评论热度加权。
- [ ] 实现 bucket 合并查询。
- [ ] 处理同分排序和游标分页。
- [ ] 过滤不可见视频。

### 9.3 热门 Feed

- [ ] 实现 `GET /api/v1/feed/hot`。
- [ ] 复用作者和互动信息批量补充。
- [ ] 与关注 Feed 共用排序和 Item DTO。
- [ ] 补充 HTTP 集成测试。

### 9.4 防击穿

- [ ] 使用 `singleflight` 合并并发缓存未命中。
- [ ] 增加本地热点缓存。
- [ ] 增加 stale cache 降级。
- [ ] 增加 TTL jitter。
- [ ] 补充并发和 Redis 故障测试。

### 9.5 浏览量回写

- [ ] 实现批量回写 Worker。
- [ ] 保证“数据库成功后删除增量”。
- [ ] 增加失败重试和指标。

## 面试追问

1. 为什么浏览计数不能每次直接 `UPDATE videos`？
2. 为什么滑动窗口比固定窗口更适合热榜？
3. 为什么热榜要用 Redis ZSET，而不是普通 Set？
4. `ZUNIONSTORE` 合并大量 bucket 时有什么性能风险？
5. 为什么取消点赞和删除评论会改变热榜分数？
6. singleflight 解决什么问题，又不能解决什么？
7. 本地缓存为什么必须设置短 TTL？
8. Redis 热榜丢失后如何从 MySQL 和互动数据恢复？
