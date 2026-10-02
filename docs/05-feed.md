# 阶段 5：Feed 主链路

## 目标

第一阶段实现关注 Feed 的纯拉模式：

```text
当前用户
-> 查询关注作者发布的视频
-> 使用复合游标稳定分页
-> 批量补充作者和点赞信息
```

暂不实现 Redis 写扩散、大 V 拉模式和热门推荐。

## 接口

```http
GET /api/v1/feed/following?cursor=<opaque>&page_size=20
Authorization: Bearer <jwt>
```

成功响应：

```json
{
  "items": [
    {
      "id": 100,
      "author": {
        "id": 10,
        "username": "user001"
      },
      "title": "video title",
      "description": "video description",
      "content_type": "video/mp4",
      "created_at": "2026-10-01T12:00:00+08:00",
      "like_count": 12,
      "is_liked_by": true
    }
  ],
  "next_cursor": "opaque-cursor",
  "has_more": true
}
```

## 查询模型

Feed 是独立的读模型，可以 JOIN follows 和 videos：

```sql
SELECT v.id, v.author_id, v.title, v.description,
       v.content_type, v.created_at
FROM videos AS v
JOIN follows AS f
  ON f.followee_id = v.author_id
WHERE f.follower_id = ?
  AND (v.created_at, v.id) < (?, ?)
ORDER BY v.created_at DESC, v.id DESC
LIMIT ?;
```

第一页不添加复合游标条件。

相关索引：

```sql
KEY idx_videos_author_created_id (author_id, created_at, id)
KEY idx_follows_followee (followee_id, follower_id)
```

## 游标

游标包含：

```json
{
  "created_at": "2026-10-01T12:00:00.123+08:00",
  "id": 100
}
```

服务端将其 Base64 URL 编码后返回给客户端。客户端只保存和回传，不解析内容。

## 批量补充

为避免 N+1，Service 必须批量读取：

- 作者信息：`user.Service.GetByIDs`
- 视频点赞数：`LikeService.CountByVideoIDs`
- 当前用户点赞状态：`LikeService.LikedVideoIDsByUser`

## 当前任务

1. 完成 Feed 游标编解码。
2. 完成 `GORMRepository.ListFollowing`。
3. 给点赞模块增加批量查询能力。
4. 完成 `feed.Service` 的分页裁剪和 DTO 组装。
5. 完成 Handler、Router 和 main 接线。
6. 补 Repository、Service、Handler 和 HTTP 集成测试。

## 面试追问

1. 为什么 Feed 不能使用 OFFSET？
2. 为什么排序需要 `(created_at, id)` 而不是只按 created_at？
3. 为什么作者信息和点赞状态不能逐条查询？
4. 纯拉模式在大 V 场景下有什么问题？
5. 推拉结合如何保证最终顺序稳定？
