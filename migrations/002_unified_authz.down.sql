-- 002_unified_authz.down.sql: Revert to owner/member model

DROP INDEX idx_todos_creator_space ON todos;
DROP TABLE IF EXISTS goal_assignees;

CREATE TABLE IF NOT EXISTS goal_members (
    id              CHAR(36)        NOT NULL,
    goal_id         CHAR(36)        NOT NULL,
    user_id         VARCHAR(64)     NOT NULL,
    role            ENUM('owner','member') NOT NULL DEFAULT 'member',
    created_at      DATETIME(3)     NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_goal_user (goal_id, user_id),
    INDEX idx_goal_members_user (user_id),
    INDEX idx_goal_members_goal (goal_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE goals CHANGE COLUMN creator_id owner_id VARCHAR(64) NOT NULL;
