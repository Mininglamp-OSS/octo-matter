-- 003_permissions_upgrade.sql

-- +migrate Up
CREATE TABLE IF NOT EXISTS matter_participants (
    id         CHAR(36)    NOT NULL,
    matter_id  CHAR(36)    NOT NULL,
    user_id    VARCHAR(64) NOT NULL,
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_matter_participant (matter_id, user_id),
    INDEX idx_participant_user (user_id),
    CONSTRAINT fk_mp_matter FOREIGN KEY (matter_id)
        REFERENCES matters(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS matter_channels (
    id            CHAR(36)         NOT NULL,
    matter_id     CHAR(36)         NOT NULL,
    channel_id    VARCHAR(255)     NOT NULL,
    channel_type  TINYINT UNSIGNED NOT NULL,
    channel_name  VARCHAR(200)     NULL,
    linked_by     VARCHAR(64)      NOT NULL,
    created_at    DATETIME(3)      NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_matter_channel (matter_id, channel_id),
    INDEX idx_mc_channel (channel_id),
    CONSTRAINT fk_mc_matter FOREIGN KEY (matter_id)
        REFERENCES matters(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +migrate Down
DROP TABLE IF EXISTS matter_channels;
DROP TABLE IF EXISTS matter_participants;
