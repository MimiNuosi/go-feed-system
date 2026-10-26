CREATE TABLE messages (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    sender_id BIGINT UNSIGNED NOT NULL,
    receiver_id BIGINT UNSIGNED NOT NULL,
    content VARCHAR(2000) NOT NULL,
    read_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    KEY idx_messages_sender_receiver_id (sender_id, receiver_id, id),
    KEY idx_messages_receiver_sender_id (receiver_id, sender_id, id),
    KEY idx_messages_receiver_unread (receiver_id, read_at, id),
    CONSTRAINT fk_messages_sender
        FOREIGN KEY (sender_id) REFERENCES users (id),
    CONSTRAINT fk_messages_receiver
        FOREIGN KEY (receiver_id) REFERENCES users (id)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;
