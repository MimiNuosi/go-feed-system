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
Pinia（auth / toast）
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

## 验证

```text
npm run build                    vue-tsc 类型检查 + Vite 构建，3.46s
产物                             108KB JS（gzip 42.5KB）+ 按页拆分 CSS
GET /、/video/1、/hot、/login     200，SPA 兜底生效
GET /legacy                      200，旧控制台可用
GET /api/v1/nope                 404 JSON，API 不被 SPA 吞掉
docker compose up -d --build api 容器 healthy
```

用无头 Chrome 截图核对过：首页（沉浸式 Feed + 右侧互动栏）、热榜、登录页均正常渲染。

## 已知局限

```text
1. 尚未实现 Refresh Token（access token 过期需重新登录）
2. 上传仍是整文件 multipart，分片/断点续传在 13.3
3. 没有 SSE 通知、私信、用户主页粉丝/关注列表
4. 前端只有类型检查和构建校验，没有组件级自动化测试
5. dist 需要手动 npm run build 后提交（Docker 构建不拉 Node 镜像）
```

## 下一步

```text
13.4 Refresh Token、改密、登出、头像
13.5 SSE 通知、私信
```
