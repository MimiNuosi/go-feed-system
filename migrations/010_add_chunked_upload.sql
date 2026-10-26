-- 秒传：同一作者上传相同 MD5 的文件时直接返回已有视频。
ALTER TABLE videos
    ADD COLUMN file_md5 CHAR(32) NOT NULL DEFAULT '' AFTER size_bytes,
    ADD KEY idx_videos_author_md5 (author_id, file_md5);

-- 分片上传会话：一次 init 对应一行，记录文件元信息与完成状态。
CREATE TABLE upload_sessions (
    id CHAR(36) NOT NULL,
    author_id BIGINT UNSIGNED NOT NULL,
    file_name VARCHAR(255) NOT NULL,
    file_size BIGINT UNSIGNED NOT NULL,
    file_md5 CHAR(32) NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    chunk_size INT UNSIGNED NOT NULL,
    total_chunks INT UNSIGNED NOT NULL,
    title VARCHAR(100) NOT NULL,
    description VARCHAR(1000) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'uploading',
    video_id BIGINT UNSIGNED NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
        ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY idx_upload_sessions_author (author_id, created_at),
    CONSTRAINT fk_upload_sessions_author
        FOREIGN KEY (author_id) REFERENCES users (id)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;

-- 已收到的分片。唯一键让"重复上传同一片"变成幂等操作。
CREATE TABLE upload_chunks (
    upload_id CHAR(36) NOT NULL,
    chunk_index INT UNSIGNED NOT NULL,
    size_bytes BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (upload_id, chunk_index),
    CONSTRAINT fk_upload_chunks_session
        FOREIGN KEY (upload_id) REFERENCES upload_sessions (id) ON DELETE CASCADE
) ENGINE=InnoDB
  DEFAULT CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;
