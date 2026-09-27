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