# 阶段 4：用户互动

阶段 4 拆分为：

```text
4.1 关注 / 取消关注
4.2 视频点赞 / 取消点赞
4.3 视频评论
```

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
