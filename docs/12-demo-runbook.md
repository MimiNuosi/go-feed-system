# 阶段 12：演示手册（5 分钟走完全链路）

## 这份文档解决什么

项目有 11 篇设计文档，但缺一份"照着做就能看到系统在跑"的走位。
这份手册有两个用途：面试演示前 5 分钟自检环境；把 README 里的性能与
设计说法变成当场可复现的证据。

前提只有一个：`docker compose up -d --build` 起完七个容器，控制台在
`http://127.0.0.1:<API_HOST_PORT>/`（默认端口见 README）。

## 路线 A：命令行走查（推荐先跑）

```powershell
pwsh -File deploy/demo/walkthrough.ps1 -SampleVideo D:\path\to\clip.mp4
```

脚本会当场注册两个账号（作者 A、观看者 B），用 B 的身份把控制台依赖的
每个接口都调一遍。`-SampleVideo` 要传一个真实的 mp4：上传接口会做内容嗅探，
随便改后缀的假文件会被拒。

它会依次验证：

```text
基础设施   /livez、/readyz、/metrics、Prometheus 抓取状态、控制台可访问
作者 A     注册 -> 登录 -> /users/me -> multipart 上传 -> 状态转 ready
           -> /feed/latest 命中 -> /videos/:id/file 返回 206（Range 生效）
观看者 B   /view -> 等回写后播放量 +1 -> 重复浏览被去重
           -> 点赞 -> 评论 -> 关注 -> /feed/following 命中
           -> /feed/hot?window=1h 命中 -> /users/:id/videos
```

最后一次实跑输出（22 步全过）：

```text
PASS  GET /readyz :: HTTP 200 status=ok
PASS  Prometheus 抓取目标 :: go-feed-system-api=up
PASS  上传视频（multipart + 内容嗅探） :: id=93 status=ready size=1318054
PASS  GET /feed/latest 命中刚发布的视频 :: items=5 命中=93
PASS  GET /videos/:id/file 支持 Range :: HTTP 206 bytes=1024
PASS  等一个回写周期后播放量 +1 :: view_count=1
PASS  同一用户重复浏览被 5 分钟去重 :: view_count=1（应保持不变）
PASS  GET /feed/following 命中关注作者的作品 :: items=1 命中=1
通过 22 步，失败 0 步
```

脚本的退出码在失败时是 1，所以可以直接当冒烟测试用（CI 里需要先起
Compose，目前没接）。

## 路线 B：浏览器点击顺序

命令行证明了接口是对的，浏览器这一步证明"人真的能用"。用刚注册的两个
账号登录控制台，按这个顺序点，每一步括号里是该看到的结果：

```text
1.  用作者 A 登录 -> 上传一段视频（提示上传成功，带视频 id）
2.  切「我的视频」（新视频在最前）
3.  切「最新」（新视频排在第一位；这是"刚发布没人关注也能被发现"的入口）
4.  打开详情 -> 点播放，播满 3 秒（提示"已记录浏览，约 6 秒后播放量刷新"，
    再等几秒详情里的播放量 +1）
5.  点赞（按钮变"取消点赞"，点赞数 +1）
6.  发表一条评论（评论列表出现该条）
7.  点「关注作者」-> 返回列表切「关注 Feed」（出现该作者的作品）
8.  切「热榜」选 1h（刚互动过的视频出现在榜单里）
9.  退出登录，用观看者 B 登录，重复 4-8（验证身份隔离：B 的点赞状态、
    关注 Feed 与 A 互不影响）
```

## 演示时最容易卡住的两个反直觉点

**播放量不是立刻变的。** `/view` 只把增量写进 Redis，MySQL 由回写 Worker
每 5 秒批量更新一次，所以点完最多等一个周期才看得到 +1。这不是卡了，
见 [docs/11](11-minimal-console.md) 里"播放量为什么看完不涨"。

**同一个用户对同一个视频 5 分钟内只算一次。** 想现场演示"播放量会涨"，
要换账号或换视频，否则你会看到一个正确的"不涨"。

## 演示时配套讲的四个设计点

```text
推拉结合   普通作者写扩散进粉丝收件箱，大 V 改在读时拉取再合并
游标分页   复合游标 (created_at, id) / (score, videoID)，同分同毫秒不重不漏
异步与一致 事务性 Outbox + RabbitMQ，消费端重连监督、重试与死信队列
性能优化   详情接口 SQL 12 -> 3、热榜匿名请求 3.01 -> 0.01，都有受控 A/B
```

Grafana 面板（`http://127.0.0.1:<GRAFANA_HOST_PORT>/`，Dashboard 名
`Go Feed System`）和 `/metrics` 适合在讲完功能后打开，证明"这些指标是
真的在采集"，而不是贴在图里的数字。

## 已知副作用

走查会往开发库里写入 `demoA*/demoB*` 账号、视频和评论。这是有意为之
（每条都要真实落库才能验证），但库会越来越脏：

```powershell
# 想从零开始，重建空库（会删掉所有数据）
docker compose down -v
docker compose up -d --build
```

## 面试追问

1. 你的冒烟脚本为什么用注册新账号而不是复用固定账号？
2. 为什么播放量断言要等 7 秒而不是 1 秒？等待时间是怎么定的？
3. 这套脚本能替代集成测试吗？两者的边界在哪？
4. 如果只允许你演示一条链路，你选哪条？为什么？
