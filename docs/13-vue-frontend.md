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
13.2 用户主页统计、粉丝/关注列表、我赞过的视频
13.3 分片上传（init / upload / status / complete）+ MD5 秒传 + 断点续传
13.4 Refresh Token、改密、登出、头像
```
