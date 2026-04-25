-- 002_unified_authz.up.sql: Unify permission model (creator + assignees)

-- 1. Rename goals.owner_id -> creator_id
ALTER TABLE goals CHANGE COLUMN owner_id creator_id VARCHAR(64) NOT NULL;

-- 2. Drop goal_members, replace with goal_assignees
DROP TABLE IF EXISTS goal_members;

CREATE TABLE IF NOT EXISTS goal_assignees (
    id              CHAR(36)                    NOT NULL,
    goal_id         CHAR(36)                    NOT NULL,
    user_id         VARCHAR(64)                 NOT NULL,
    created_at      DATETIME(3)                 NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_goal_user (goal_id, user_id),
    INDEX idx_goal_assignees_user (user_id),
    INDEX idx_goal_assignees_goal (goal_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 3. Add visibility index for todos (used by scoped list query)
CREATE INDEX idx_todos_creator_space ON todos(space_id, creator_id);
