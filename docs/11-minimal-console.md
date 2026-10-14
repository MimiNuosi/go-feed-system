# 阶段 11：最小前端控制台（人工验证）

## 目标与取舍

目标是让"项目到底能不能正常运行"变成**能点、能看**的事，而不是只靠 curl
和单元测试。之前每次验证都要手写一堆请求，而且没法直观看到 Feed 与热榜的样子。

参考项目（`LeoninCS/feedsystem_video_go`）有一个完整前端：Vue 3 + Vite + TS +
Pinia + vue-router，nginx 部署，11 个页面（首页、Feed、热榜、视频详情、私信、
个人主页、设置……）。但它的接口面比我们大：**token 刷新、分片上传、封面/头像
上传、私信、按作者列视频**这些我们都没有。照抄页面会做出一堆点不动的界面 ——
前端的形态是被后端能力决定的。

所以第一版做成：**单文件静态页（原生 HTML + fetch），由 API 同源提供**。

## 交付

```text
internal/web/index.html     页面本体：登录/注册、上传、关注 Feed、热榜、
                            详情（播放/点赞/评论/关注/记录浏览）
internal/web/web.go         go:embed 把页面打进二进制
internal/router/router.go   GET / 返回该页面
```

三个决定及理由：

```text
go:embed 而不是读磁盘
  镜像里只有一个二进制、不用挂载目录，Dockerfile 也不用改
  （构建阶段本来就 COPY 了整个仓库）。代价是改页面要重新编译，
  对"验证工具"来说可以接受。
同源提供而不是单独的 nginx
  不需要配 CORS、不需要代理、不需要多一个容器；
  生产环境应该交给 nginx/CDN，这里是为了开箱即用。
原生 fetch 而不是 Vue
  验证工具不需要构建链、路由和状态管理；需要的是"打开就能点"。
  代价是它讲不了前端工程化，所以不能当成"全栈"的论据。
```

## 覆盖的人工验证流程（六条）

```text
1. 注册 / 登录（401 时自动清登录态并提示）
2. 上传视频（multipart，走内容嗅探那条路）
3. 关注 Feed（复合游标 + "加载更多"）
4. 热榜（1h / 24h 切换 + 游标翻页）
5. 详情页：<video> 播放（会触发 Range 请求）、点赞/取消、评论增删、关注作者
6. 记录一次浏览 —— 热榜里立刻能看到这个视频，形成可见闭环
```

## 验证结果

API 层（按页面的调用顺序逐条跑通）：

```text
注册 -> 登录 -> /users/me -> 上传(id=69, ready) -> 详情 -> Range 206
-> 浏览 204 -> 点赞 204 -> 评论 -> 关注 204 -> 关注 Feed 有数据
-> 热榜 1h 出现该视频
```

浏览器层（真实点击，用无障碍树与截图核对）：

```text
注册成功 -> 登录成功 -> 热榜渲染出真实数据（"UI 验证视频 @ui3121752 播放 1 点赞 1"）
-> 打开详情（播放器渲染、点赞后按钮变"取消点赞"、点赞数 1→2、评论列表与发表框可用）
-> 关注 Feed 空状态正确
```

## 顺手修掉的一个回归（值得记）

**现象**：上传接口返回 500 `INTERNAL`，而阶段 9 的端到端测试里它是好的。

**根因**：`outbox_events.payload` 是 JSON 列，而我在阶段 10 的压测优化里给
DSN 加了 `interpolateParams=true`。这个开关让 go-sql-driver 在客户端做参数转义，
**`[]byte` 参数会被写成 `_binary'...'`（binary 字符集）**，MySQL 拒绝用它构造
JSON 值：

```text
Error 3144 (22032): Cannot create a JSON value from a string with CHARACTER SET 'binary'
```

驱动源码可以确认这个分支（`connection.go`）：`[]byte` → `_binary'...'`，
`string` → 使用连接字符集。**修复**：把 `outbox.Event.Payload` 从 `[]byte`
改成 `string`（语义上它本来就是 JSON 文本），投递给 RabbitMQ 时再转回 `[]byte`。

为什么压测没发现：**压的全是读接口**，写路径根本没被覆盖。
为什么集成测试也没发现：集成测试的 DSN 来自 `TEST_MYSQL_DSN`，
而我手动拼的 DSN 没带 `interpolateParams=true`，**和生产不一致**。

两条教训：

```text
1. 压测要覆盖写路径，否则"性能没问题"和"功能没问题"是两件独立的事。
2. 集成测试的 DSN 必须和生产同款参数，否则驱动层面的行为差异测不出来。
```

所以现在的验证命令是带参数的（这也是以后跑集成测试的推荐姿势）：

```powershell
$env:TEST_MYSQL_DSN='feed_app:<pass>@tcp(127.0.0.1:13308)/go_feed?charset=utf8mb4&parseTime=True&loc=Local&interpolateParams=true'
go test -count=1 ./internal/outbox/ ./internal/video/
```

## 已知局限

```text
1. 页面只覆盖我们已有的接口；没有搜索、私信、分片上传、头像/封面
2. 单文件内联脚本，没有 CSP；生产环境应交给 nginx/CDN 并用独立的前端工程
3. 上传是整文件 multipart（后端也只支持这个），大文件场景需要改分片
```

## 面试追问

1. 为什么前端由 Go 服务直接提供？生产环境该怎么做？
2. `go:embed` 与读磁盘文件各有什么取舍？
3. `interpolateParams` 带来了什么好处、又踩了什么坑？为什么只有写路径会炸？
4. 你的压测覆盖写接口了吗？如果没覆盖，能得出什么结论、不能得出什么结论？

## 当前任务

- [x] 单文件控制台 + `go:embed` + 同源提供
- [x] 六条流程的 API 层与浏览器层验证
- [x] 修复 outbox JSON 列的写入回归，并用生产同款 DSN 验证集成测试
- [ ] （可选）升级成 Vue/Vite 工程，覆盖"全栈"投递面
- [ ] （可选）补"关注列表 / 我的视频"等页面（需要先加后端接口）
