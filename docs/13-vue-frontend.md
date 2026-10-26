# 阶段 13：Vue 3 产品前端

## 目标与取舍

阶段 11 的内嵌控制台解决的是"能人工验证"，但演示时看起来像测试页。这一阶段补一个仿短视频产品的消费者端前端：

```text
1. 首页是沉浸式竖屏 Feed，不是表单堆叠
2. 覆盖「浏览 → 播放 → 点赞 → 评论 → 关注 → 发布 → 我的作品」闭环
3. 不引入额外容器：构建产物用 go:embed 打进 API 二进制，与 API 同源
4. 旧控制台保留在 /legacy 作为零依赖兜底
```

前端不作为学习目标，按产品标准直接落地；后端仍遵循原有分层与依赖注入约定。

## 技术栈

```text
Vue 3 + TypeScript + Vite 7
vue-router 4（history 模式）
Pinia（auth / toast / notifications）
lucide-vue-next（图标）
手写 CSS（不引 UI 框架）
```

## 页面与接口映射

| 路由 | 页面 | 主要接口 |
| --- | --- | --- |
| `/` | 首页（最新 Feed，沉浸式） | `GET /feed/latest`、`POST /videos/:id/view` |
| `/following` | 关注 Feed（需登录） | `GET /feed/following` |
| `/hot` | 热榜（1h / 24h） | `GET /feed/hot` |
| `/video/:id` | 视频详情 | 详情、点赞、评论、关注 |
| `/upload` | 发布作品（需登录） | `POST /videos`（multipart，带进度） |
| `/me` | 我的作品（需登录） | `GET /users/me`、`GET /users/:id/videos` |
| `/u/:id` | 用户主页 | `GET /users/:id/profile`、粉丝/关注列表、`/users/:id/videos` |
| `/notifications` | 通知中心（需登录） | 通知列表、未读数、全部已读、SSE 在线推送 |
| `/messages`、`/messages/:peerId` | 私信会话（需登录） | 会话列表、历史消息、发送、已读、SSE 提醒 |
| `/login`、`/register` | 登录 / 注册 | `POST /auth/login`、`POST /auth/register` |

## 关键实现

**沉浸式 Feed**：容器 `scroll-snap-type: y mandatory`，每条视频占满一屏；
`IntersectionObserver` 只让当前可见条目播放，其余暂停；播放满 3 秒调用一次 `/view`。

**无限滚动**：游标分页，滚动到"还剩 2 条"时用 `next_cursor` 拉下一页。

**鉴权**：access token 存 localStorage，API client 自动加 `Authorization`；
401 时清 token 并派发 `auth:expired`，路由跳登录并带 `redirect`。

**上传进度**：`fetch` 拿不到上传进度，发布页用 `XMLHttpRequest.upload.onprogress` 驱动进度条。

**服务端托管**：`internal/web/web.go` 用 `go:embed dist` 内嵌产物；
`internal/router/router.go` 注册 `/assets/*` 与 SPA 兜底（刷新 `/video/1`、`/login` 会回退到 `index.html`），
`/legacy` 继续提供旧控制台。

**用户主页（13.2）**：`/u/:id` 展示作品数、粉丝/关注数、获赞数，以及作品 / 粉丝 / 关注 /
喜欢四个 Tab（"喜欢"仅自己可见）。后端新增 `internal/profile` 读模型：

```text
GET /api/v1/users/:id/profile        用户资料 + 四项计数 + is_following / is_me
GET /api/v1/users/:id/followers      粉丝列表（follows.id 倒序游标）
GET /api/v1/users/:id/following      关注列表（follows.id 倒序游标）
GET /api/v1/users/me/liked-videos    我赞过的视频（likes.id 倒序游标，仅自己）
```

迁移 `009_add_profile_indexes.sql` 补了 `(followee_id,id)`、`(follower_id,id)`、`(user_id,id)`
三个复合索引，让列表分页不再 filesort。

## 13.3 分片上传（5MB + MD5 秒传 + 断点续传）

参考项目里这块是重点，实现上与 C++ 版的"按 seq 计算 offset 直接写最终文件"不同：
Go 版把每片存成独立文件，complete 时按序合并，因此支持乱序/并发上传。

接口（全部需要登录）：

```text
POST /api/v1/videos/chunk/init      创建会话；命中秒传直接返回 video_id
POST /api/v1/videos/chunk/upload    multipart：upload_id / chunk_index / file
GET  /api/v1/videos/chunk/status    返回已上传分片，用于断点续传
POST /api/v1/videos/chunk/complete  校验齐全 -> 合并 -> 复用整文件上传 -> 写回 video_id
```

数据与存储：

```text
migrations/010  videos.file_md5 + (author_id, file_md5) 索引
                upload_sessions（一次上传一行）
                upload_chunks（主键 upload_id + chunk_index，重复上传幂等）
临时文件        <LOCAL_STORAGE_DIR>/uploads/<upload_id>/<index>.part
分片大小        默认 5MB，允许 1~10MB
秒传范围        同一作者 + 同一 MD5
断点续传        同一作者 + 同一 MD5 的 uploading 会话复用，init 返回已传分片
```

正确性校验：

```text
每片大小必须与预期一致（最后一片按 file_size 计算）
合并时重新计算整文件 MD5，与 init 上报值不一致直接拒绝
complete 幂等：已完成直接返回 video_id；崩溃后可凭 MD5 找回并补标
上传目录在成功后整体清理
```

前端发布页新增「分片上传 / 普通上传」切换：`spark-md5` 计算文件 MD5（分片读取，不整文件进内存），
init 后只上传缺失分片，3 个并发 + 每片 3 次重试，进度条展示 MD5（0-20%）、
分片上传（20-85%）、合并（90-100%）三个阶段。

实测：

```text
1.3MB 文件 / 1MB 分片 -> 2 片；init -> 传 2 片 -> status [0,1] -> complete -> video 94
同一 MD5 再次 init -> 直接返回 video_id=94（秒传）
只传 1 片就 complete -> 409
中断后再次 init -> 返回同一 upload_id 与 uploaded=[0]（断点续传），补传第 2 片后 complete 成功
go test ./internal/upload -> 通过（合并内容、MD5 传递、秒传、不完整上传）
```

## 13.4 Refresh Token、改密、登出与头像

**Refresh Token**：登录返回 access + refresh 两枚令牌。refresh 是 32 字节随机值，
数据库 `refresh_tokens` 只存 SHA-256 哈希；有效期 30 天，每次刷新都轮换
（旧 refresh 立即吊销），改密时吊销该用户全部 refresh。

```text
POST /api/v1/auth/refresh   {refresh_token} -> 新 access + 新 refresh（旧 refresh 失效）
POST /api/v1/auth/logout    {refresh_token} -> 204（幂等）
POST /api/v1/users/me/password {old_password,new_password} -> 204，并吊销全部 refresh
```

前端 `api.ts` 在收到 401 时自动用 refresh 换新令牌并重试原请求；refresh 也失败才清 token
跳登录。并发请求共用一个 refresh promise，避免同一时刻发起多次刷新。

**头像**：`POST /users/me/avatar`（multipart）上传，服务端校验 ≤2MB、
用 `http.DetectContentType` 嗅探 jpeg/png/webp，存到 `avatars/<uuid>.<ext>`，
并更新 `users.avatar_key`。`GET /users/:id/avatar` 公开读取，带 5 分钟缓存。
用户主页与 `/users/me` 返回 `avatar_url`。

前端新增 `/settings`：更换头像、修改密码（成功后强制重新登录）、退出登录。

实测：

```text
refresh 轮换：旧 refresh 再用 -> 401
改密：旧密码登录 -> 401，新密码登录 -> 200
登出：refresh 再用 -> 401
头像：上传 64x64 PNG -> avatar_key，GET 返回 image/png 200，profile.avatar_url 正确
用户主页渲染出头像（无头 Chrome 截图核对）
```

## 13.5 SSE 通知

点赞、评论、关注成功后写入 `notifications`，再通过 SSE 推给在线用户。
通知写入采用 best-effort：失败只记服务端日志，不回滚已经成功的点赞、评论或关注；
离线时也不会丢，因为数据库记录才是事实来源，SSE 只是低延迟通道。

```text
GET  /api/v1/notifications                游标分页（id 倒序）
GET  /api/v1/notifications/unread-count   未读数
POST /api/v1/notifications/read-all       全部已读
GET  /api/v1/notifications/stream         text/event-stream
```

设计要点：

```text
1. 自己操作自己不写通知
2. Service 批量查询 actor，避免通知列表 N+1
3. Hub 按 userID 维护单实例订阅；慢订阅者缓冲满时丢在线事件，不阻塞业务写入
4. SSE 有空事件心跳；客户端 context.Done 时取消订阅
5. 前端用 fetch 读流，不用 EventSource，避免把 Bearer Token 放进 URL
6. 多实例部署时 Hub 不共享，需要 Redis Pub/Sub 或 RabbitMQ fanout 广播
```

实测：

```text
B 关注 / 点赞 / 评论 A 的视频 -> A 的 SSE 收到 3 个事件
离线通知列表 kinds = comment,like,follow，actor.username = demoB59372
read-all 后未读数 0；A 自己点赞自己的视频不会新增通知
go test ./internal/notification ./internal/interaction ./internal/router 通过
```

## 13.5 私信

私信采用"MySQL 存事实 + SSE 做在线提醒"的模型。消息先成功落库，再广播给
收件人；即使收件人不在线、SSE 断开或慢订阅者被丢弃，重新打开会话仍能看到
全部消息和未读数。

```text
POST /api/v1/messages                    发送消息
GET  /api/v1/messages/conversations      最近会话与每个会话未读数
GET  /api/v1/messages/unread-count       私信总未读数
GET  /api/v1/messages/:peerId            历史消息，id 游标向前翻页
POST /api/v1/messages/:peerId/read       把该会话内对方发来的消息标记已读
GET  /api/v1/messages/stream             SSE 实时提醒
```

实现细节：

```text
1. 禁止给自己发消息；内容 trim 后 1~2000 字符
2. 历史查询按 id 倒序多取一条判断 has_more，Service 再反转成旧 -> 新返回
3. 会话列表用每个 peer 的 MAX(id) 取最后一条，再批量查用户和未读数，避免 N+1
4. 页面打开会话后标记已读并重新同步总未读数
5. 用户主页的"私信"直接进入 /messages/:peerId
6. 通知和私信共用同一套 fetch SSE 解析，但事件类型与 Hub 相互隔离
```

实测：

```text
B -> A 连续发送 3 条：A 的 SSE 收到 3 个 message 事件
未读数 3；会话列表 peer=demoB59372、unread_count=3
page_size=2 返回最新两条且 has_more=true，cursor 补出第一条
标记会话已读后总未读数 0
给自己发返回 400，收件人不存在返回 404
桌面 1440px 与手机 390px 截图核对，无横向溢出
```

## 验证

```text
npm run build                    vue-tsc 类型检查 + Vite 构建通过
产物                             按页面拆包，通知页/私信页独立 chunk
GET /、/video/1、/hot、/login     200，SPA 兜底生效
GET /legacy                      200，旧控制台可用
GET /api/v1/nope                 404 JSON，API 不被 SPA 吞掉
```

用无头 Chrome 截图核对过：首页（沉浸式 Feed + 右侧互动栏）、热榜、登录页均正常渲染。

## 已知局限

```text
1. 通知与私信 Hub 都是单实例内存广播，多实例需要 Redis Pub/Sub 或 RabbitMQ fanout
2. 前端只有类型检查、构建校验和截图冒烟，没有组件级自动化测试
3. dist 需要手动 npm run build 后提交（Docker 构建不拉 Node 镜像）
```

## 下一步

```text
阶段 13 收尾：全量回归、演示账号与简历材料同步
```
