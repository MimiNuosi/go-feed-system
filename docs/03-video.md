# 阶段 3：视频上传与元数据

## 已确定的设计

- 第一版使用本地磁盘，但通过 ObjectStorage 接口解耦。
- 上传协议使用 multipart/form-data。
- 第一版不引入 FFmpeg、缩略图、转码和时长解析。
- MySQL 只保存视频元数据，视频二进制保存到文件系统。
- 上传接口需要 JWT 鉴权。

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