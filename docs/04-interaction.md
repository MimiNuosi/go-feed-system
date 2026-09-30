# 阶段 4：用户互动

阶段 4 拆分为：

```text
4.1 关注 / 取消关注
4.2 视频点赞 / 取消点赞
4.3 视频评论
```

## 4.2 视频点赞

### 接口设计

写操作使用独立接口：

```http
POST   /api/v1/videos/:id/like
DELETE /api/v1/videos/:id/like
```

两个接口都需要 JWT。成功统一返回 `204 No Content`。

视频详情和 Feed 后续再展示：

```json
{
  "like_count": 12,
  "is_liked_by": true
}
```

`is_liked_by` 是“视频 + 当前用户”的组合结果，不是 `videos` 表字段。
公开详情如果要返回它，需要先增加“可选鉴权”：没有 Token 时按匿名用户处理。

### 建表 SQL

按照项目约束，Codex 不自动修改 migrations。你需要自己保存并执行迁移。

```sql
CREATE TABLE likes (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    user_id BIGINT UNSIGNED NOT NULL,
    video_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_likes_user_video (user_id, video_id),
    KEY idx_likes_video_user (video_id, user_id),
    CONSTRAINT fk_likes_user
        FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT fk_likes_video
        FOREIGN KEY (video_id) REFERENCES videos (id)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;
```

### 设计决策

1. 重复点赞是幂等成功，不返回 409。
2. 取消未点赞的视频同样返回成功。
3. 使用 `UNIQUE(user_id, video_id)` 防止并发重复插入。
4. 第一阶段使用 `COUNT(*)`，不维护 `videos.like_count` 冗余字段。
5. `INDEX(video_id, user_id)` 用于按视频统计点赞数。

### 当前任务

1. 自己创建并执行 likes 表迁移。
2. 完成 `GORMLikeRepository` 中的 TODO。
3. 完成 `LikeService` 的幂等点赞、取消点赞和状态组合。
4. 为 Repository、Service 和 Handler 补测试。
5. 接入 Router 和 main。
6. 将点赞状态接入视频详情，并思考公开接口的可选鉴权。

### 面试追问

1. 为什么点赞不能先 `Exists` 再 `Create`？
2. 为什么重复点赞要返回成功，而不是 409？
3. `UNIQUE(user_id, video_id)` 和 `INDEX(video_id, user_id)` 分别服务什么查询？
4. 为什么第一阶段不用 Redis 计数器？

## 关注关系

关注是单向关系：

```text
A 关注 B，只保存 A -> B
```

判断互相关注时，查询反向记录是否存在，不重复保存两条关注数据。

## 建表 SQL

按照项目约束，Codex 不自动修改 migrations。你需要自己保存并执行迁移。

```sql
CREATE TABLE follows (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    follower_id BIGINT UNSIGNED NOT NULL,
    followee_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_follows_pair (follower_id, followee_id),
    KEY idx_follows_followee (followee_id, follower_id),
    CONSTRAINT chk_follows_not_self CHECK (follower_id <> followee_id),
    CONSTRAINT fk_follows_follower
        FOREIGN KEY (follower_id) REFERENCES users (id),
    CONSTRAINT fk_follows_followee
        FOREIGN KEY (followee_id) REFERENCES users (id)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;
```

## 计划接口

```http
POST   /api/v1/users/:id/follow
DELETE /api/v1/users/:id/follow
GET    /api/v1/users/:id/follow/status
```

写操作需要 JWT。

## C++ 对照

你的 C++ 好友模块使用 friend_apply 处理申请，并在通过后写入两条 friend
关系表示双向好友。关注没有申请和确认阶段，只需要保存一条单向关系。

## 当前任务

1. 自己创建并执行 follows 表迁移。
2. 完成 GORMFollowRepository 中的 TODO。
3. 完成 FollowService 的幂等关注和取消关注。
4. 为 Repository、Service 和 Handler 补测试。
5. 接入 Router 和 main。

## 面试追问

1. 为什么关注操作要做成幂等？
2. 为什么推荐唯一索引而不是先查询再插入？
3. 联合唯一索引和反向索引分别服务什么查询？
4. 为什么第一阶段使用 COUNT(*) 而不维护冗余计数？
