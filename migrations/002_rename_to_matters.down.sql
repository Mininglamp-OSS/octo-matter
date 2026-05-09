-- 002_rename_to_matters.down.sql

-- Revert index names
ALTER TABLE matter_assignees DROP INDEX uk_matter_user;
ALTER TABLE matter_assignees ADD UNIQUE KEY uk_todo_user (matter_id, user_id);
ALTER TABLE matter_assignees DROP INDEX idx_assignees_matter;
ALTER TABLE matter_assignees ADD INDEX idx_assignees_todo (matter_id);

-- Revert foreign keys and column names
ALTER TABLE matter_comment_attachments DROP FOREIGN KEY fk_matter_comment_attachments_comment;
ALTER TABLE matter_comment_attachments ADD CONSTRAINT fk_comment_attachments_comment FOREIGN KEY (comment_id) REFERENCES matter_comments(id) ON DELETE CASCADE;

ALTER TABLE matter_comments DROP FOREIGN KEY fk_matter_comments_matter;
ALTER TABLE matter_comments CHANGE matter_id todo_id CHAR(36) NOT NULL;
ALTER TABLE matter_comments ADD CONSTRAINT fk_todo_comments_todo FOREIGN KEY (todo_id) REFERENCES matters(id) ON DELETE CASCADE;

ALTER TABLE matter_assignees DROP FOREIGN KEY fk_matter_assignees_matter;
ALTER TABLE matter_assignees CHANGE matter_id todo_id CHAR(36) NOT NULL;
ALTER TABLE matter_assignees ADD CONSTRAINT fk_todo_assignees_todo FOREIGN KEY (todo_id) REFERENCES matters(id) ON DELETE CASCADE;

-- Rename tables back
RENAME TABLE matters TO todos;
RENAME TABLE matter_assignees TO todo_assignees;
RENAME TABLE matter_comments TO todo_comments;
RENAME TABLE matter_comment_attachments TO todo_comment_attachments;

-- Revert status enum (widen→backfill→narrow to avoid strict-mode failures)
-- Step 1: Widen enum to superset
ALTER TABLE todos MODIFY status ENUM('open','closed','done','archived') NOT NULL DEFAULT 'open';

-- Step 2: Backfill (revert done → closed, archived → open)
UPDATE todos SET status = 'closed' WHERE status = 'done';
UPDATE todos SET status = 'open' WHERE status = 'archived';

-- Step 3: Narrow to original shape
ALTER TABLE todos MODIFY status ENUM('open','closed') NOT NULL DEFAULT 'open';

-- Add goal_id back
ALTER TABLE todos ADD COLUMN goal_id CHAR(36) NULL AFTER space_id;
ALTER TABLE todos ADD INDEX idx_todos_goal (goal_id);

-- Recreate goal tables
CREATE TABLE IF NOT EXISTS goals (
    id              CHAR(36)        NOT NULL,
    space_id        VARCHAR(64)     NOT NULL,
    title           VARCHAR(200)    NOT NULL,
    description     TEXT            NULL,
    creator_id      VARCHAR(64)     NOT NULL,
    status          ENUM('active','completed','archived') NOT NULL DEFAULT 'active',
    deadline        DATETIME(3)     NULL,
    created_at      DATETIME(3)     NOT NULL,
    updated_at      DATETIME(3)     NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_goals_space_creator (space_id, creator_id),
    INDEX idx_goals_space_status (space_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS goal_assignees (
    id              CHAR(36)        NOT NULL,
    goal_id         CHAR(36)        NOT NULL,
    user_id         VARCHAR(64)     NOT NULL,
    created_at      DATETIME(3)     NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_goal_user (goal_id, user_id),
    INDEX idx_goal_assignees_user (user_id),
    INDEX idx_goal_assignees_goal (goal_id),
    CONSTRAINT fk_goal_assignees_goal FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE todos ADD CONSTRAINT fk_todos_goal FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE SET NULL;
