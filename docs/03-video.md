# 阶段 3：视频上传与元数据

## 已确定的设计

- 第一版使用本地磁盘，但通过 ObjectStorage 接口解耦。
- 上传协议使用 multipart/form-data。
- 第一版不引入 FFmpeg、缩略图、转码和时长解析。
- MySQL 只保存视频元数据，视频二进制保存到文件系统。
- 上传接口需要 JWT 鉴权。
- 上传类型校验以**服务端内容嗅探**为准，不信任客户端声明的 Content-Type。

## 上传类型校验（已实现）

三层，权威性从低到高：

```text
第 1 层 扩展名白名单  mp4 / m4v / mov / webm / 3gp
                    便宜，用于快速拒掉明显不对的文件
第 2 层 客户端声明    只放行 video/* 和 application/octet-stream
                    很多 HTTP 客户端（Go multipart 默认、PowerShell -Form）
                    都会发 octet-stream，拒绝它们只会误伤老实客户端
第 3 层 内容嗅探      唯一裁决者，决定入库的 MIME 和 StorageKey 的后缀
```

嗅探只读文件头前 512 字节，并且必须“只看不消费”：

```go
reader := bufio.NewReader(input.Content)
header, err := reader.Peek(512) // Peek 不会推进游标
```

ISO-BMFF（mp4/mov/m4v/3gp）的 `ftyp` box 大端布局：

```text
offset 0   4 字节  box 总长度
offset 4   4 字节  固定为 "ftyp"
offset 8   4 字节  major_brand
offset 12  4 字节  minor_version（不是 brand，要跳过）
offset 16  起      每 4 字节一个 compatible brand
```

`.m4v`/`.m4a` 与 `.mp4` 是同一个容器，统一归并为 `video/mp4`。
WebM 用 EBML 魔数 `1A 45 DF A3` 识别。

刻意不使用 `http.DetectContentType`：它的契约是“浏览器该怎么渲染这段字节”，
只在一串 brand 里搜到字面量 `mp4` 才返回 `video/mp4`，因此会把
`ffmpeg -brand avc1` 生成的真 mp4、QuickTime、3gp 全判成未知类型。

扩展名和嗅探结果不一致时按内容放行，但打 `WARN` 日志留痕。

## 目录结构

```text
internal/video/
  errors.go
  model.go
  storage.go
  repository.go
  service.go
  handler.go
```

后续本地磁盘实现将放在：

```text
pkg/storage/local/
  storage.go
```

## 计划接口

```http
POST /api/v1/videos
Authorization: Bearer <jwt>
Content-Type: multipart/form-data

title: 视频标题
description: 视频描述
file: 视频文件
```

## 建表 SQL

按照项目约束，Codex 不自动修改 migrations。你需要自己保存并执行迁移。

```sql
CREATE TABLE videos (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    author_id BIGINT UNSIGNED NOT NULL,
    title VARCHAR(100) NOT NULL,
    description VARCHAR(1000) NOT NULL DEFAULT '',
    storage_key VARCHAR(512) NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT UNSIGNED NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'ready',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
        ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_videos_storage_key (storage_key),
    KEY idx_videos_author_created_id (author_id, created_at, id),
    CONSTRAINT fk_videos_author
        FOREIGN KEY (author_id) REFERENCES users (id)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;
```

## 当前任务

1. 自己创建并执行 videos 表迁移。
2. 完成 GORMRepository 的 Create 和 FindByID。
3. 实现本地磁盘 ObjectStorage。
4. 完成 Service.Upload 的文件保存、元数据写入和失败补偿。
5. 为 multipart Handler 补充测试。

## C++ 对照

你的 ResourceServer 使用 FileService、FileWorker 和 ofstream 处理文件。
Go 会把文件存储收口到 ObjectStorage，把元数据收口到 VideoRepository，
Service 负责协调两者，避免文件系统和 MySQL 逻辑散落在 Handler 中。

## 面试追问

1. 为什么不能直接使用客户端上传的 OriginalFilename 作为磁盘路径？

防止路径穿越攻击（Path Traversal）。如果用户把文件名伪造成 ../../etc/passwd 或者 ../../mysql/data，一旦我们直接用这个文件名拼接路径去存，服务器极有可能被覆盖关键文件甚至被黑客提权。我们必须在 Service 层通过 UUID 生成一个随机的 StorageKey（比如 2026/09/uuid.mp4），而把原始文件名只作为数据库里的展示字段。

2. 文件保存成功但数据库写入失败时，应该怎样补偿？

这是经典的分布式事务/最终一致性问题。文件系统写成功、MySQL 写失败，如果不处理，就会产生“孤儿文件”。解决方案是：在 Service 层捕获数据库写入错误，然后显式调用 ObjectStorage.Delete(storageKey) 去删除刚刚落地的文件，通过“补偿事务”来保证数据的最终一致性。

3. 数据库写入成功但文件保存失败时，系统应该呈现什么状态？

这是更极端的情况（极少发生，通常是磁盘满了或权限问题）。因为此时用户拿不到真实的视频文件，数据库里却有了记录。设计上应该采用状态机：在数据库中给视频表增加一个 status 字段。只有文件和数据库都写成功，状态才更新为 ready；如果落盘失败，状态保持为 pending 或直接抛出异常回滚数据库插入。

4. 为什么 Content-Type 不能只依赖客户端传入的 Header？

客户端的 Header 是可以被任意伪造的（比如把 .exe 重命名为 .mp4 并伪造 Header）。如果在后端只信这个 Header，黑客就能上传恶意可执行文件。真正严谨的做法是：读取文件的前几个字节（魔数/Magic Number） 来判断真实的文件类型，或者调用 ffprobe 这样的工具去验证它是不是合法的视频编码格式。

本项目已经落地第一半：`SniffContainer` 解析 `ftyp` box 的 brand（或 EBML 魔数）
决定容器类型，入库的 MIME 和 StorageKey 后缀都用嗅探结果。因为暂时不引入
FFmpeg，校验只到“容器”这一层，还没有到“编码/时长/分辨率”这一层。

5. 为什么不能直接用 `http.DetectContentType`？

它的契约是“浏览器该怎么渲染这段字节”，不是“这是不是我能收下的视频容器”。
它的 mp4 签名只在一串 brand 里搜到字面量 `mp4` 才返回 `video/mp4`，所以
`ffmpeg -brand avc1` 生成的真 mp4、iTunes 的 `M4V `、QuickTime 和 3gp 全部
落到 `application/octet-stream`。用它做校验会一边放过改名伪装、一边误杀合法文件。

6. `bufio.Reader.Peek` 和 C++ 的 `seekg(0)` 是什么关系？

嗅探需要“先看头部再原样写入存储”。`io.Reader` 没有回退能力，所以不能用
seek 的思路；`Peek` 返回缓冲区里的字节但不推进游标，正好等价于“看一眼再
放回去”。如果改用 `Read` 读掉 512 字节，就必须自己拼一个 `io.MultiReader`
补回头部，很容易漏字节。
