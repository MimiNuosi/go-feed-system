-- 用户主页的粉丝/关注/喜欢列表都按各自关系的自增 id 倒序做游标分页。
-- 现有索引只能过滤，排序仍需 filesort，这里补上 (过滤列, id) 复合索引。

ALTER TABLE follows
    ADD KEY idx_follows_followee_id (followee_id, id),
    ADD KEY idx_follows_follower_id (follower_id, id);

ALTER TABLE likes
    ADD KEY idx_likes_user_id (user_id, id);
