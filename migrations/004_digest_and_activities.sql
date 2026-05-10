-- 004_digest_and_activities.sql

-- +migrate Up

-- 1. Space-scoped display sequence number
ALTER TABLE matters
  ADD COLUMN seq_no INT UNSIGNED NULL AFTER id,
  ADD UNIQUE KEY uk_space_seq (space_id, seq_no);

-- Backfill existing rows (MySQL 8.0+ window function, safe under strict mode)
UPDATE matters m
JOIN (
  SELECT id, ROW_NUMBER() OVER (PARTITION BY space_id ORDER BY created_at) AS rn
  FROM matters
) ranked ON m.id = ranked.id
SET m.seq_no = ranked.rn;

ALTER TABLE matters MODIFY seq_no INT UNSIGNED NOT NULL;

-- 2. Comment upgrades for digest support
ALTER TABLE matter_comments
  ADD COLUMN channel_id CHAR(36) NULL,
  ADD COLUMN channel_type TINYINT UNSIGNED NULL,
  ADD COLUMN source_channel_id VARCHAR(255) NULL,
  ADD COLUMN source_msgs JSON NULL,
  ADD COLUMN related_uids JSON NULL,
  ADD INDEX idx_comments_matter_channel (matter_id, source_channel_id, created_at),
  ADD CONSTRAINT fk_comments_channel
    FOREIGN KEY (channel_id) REFERENCES matter_channels(id) ON DELETE SET NULL;

-- Clean up legacy index (superseded by compound index above)
ALTER TABLE matter_comments DROP INDEX idx_comments_todo;

-- 3. Activity log
CREATE TABLE IF NOT EXISTS matter_activities (
    id          CHAR(36)    NOT NULL,
    matter_id   CHAR(36)    NOT NULL,
    actor_id    VARCHAR(64) NOT NULL,
    action      VARCHAR(50) NOT NULL,
    detail      JSON        NULL,
    created_at  DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_activity_matter (matter_id, created_at DESC),
    CONSTRAINT fk_activity_matter FOREIGN KEY (matter_id)
        REFERENCES matters(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +migrate Down
DROP TABLE IF EXISTS matter_activities;
ALTER TABLE matter_comments ADD INDEX idx_comments_todo (matter_id);
ALTER TABLE matter_comments DROP FOREIGN KEY fk_comments_channel;
ALTER TABLE matter_comments DROP INDEX idx_comments_matter_channel;
ALTER TABLE matter_comments DROP COLUMN related_uids;
ALTER TABLE matter_comments DROP COLUMN source_msgs;
ALTER TABLE matter_comments DROP COLUMN source_channel_id;
ALTER TABLE matter_comments DROP COLUMN channel_type;
ALTER TABLE matter_comments DROP COLUMN channel_id;
ALTER TABLE matters DROP INDEX uk_space_seq;
ALTER TABLE matters DROP COLUMN seq_no;
