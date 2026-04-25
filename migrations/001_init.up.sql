-- 001_init.up.sql: Create all tables for Octo Todo

CREATE TABLE IF NOT EXISTS goals (
    id              CHAR(36)        NOT NULL,
    space_id        VARCHAR(64)     NOT NULL,
    title           VARCHAR(200)    NOT NULL,
    description     TEXT            NULL,
    owner_id        VARCHAR(64)     NOT NULL,
    archived        TINYINT(1)      NOT NULL DEFAULT 0,
    created_at      DATETIME(3)     NOT NULL,
    updated_at      DATETIME(3)     NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_goals_space_owner (space_id, owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

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

CREATE TABLE IF NOT EXISTS todos (
    id                   CHAR(36)                                                       NOT NULL,
    space_id             VARCHAR(64)                                                    NOT NULL,
    goal_id              CHAR(36)                                                       NULL,
    title                VARCHAR(500)                                                   NOT NULL,
    description          TEXT                                                           NULL,
    creator_id           VARCHAR(64)                                                    NOT NULL,
    status               ENUM('draft','planned','in_progress','done','cancelled')       NOT NULL DEFAULT 'draft',
    deadline             DATETIME(3)                                                    NULL,
    remind_at            DATETIME(3)                                                    NULL,
    source_channel_id    VARCHAR(255)                                                   NULL,
    source_channel_type  TINYINT UNSIGNED                                               NULL,
    source_name          VARCHAR(200)                                                   NULL,
    created_at           DATETIME(3)                                                    NOT NULL,
    updated_at           DATETIME(3)                                                    NOT NULL,
    deleted_at           DATETIME(3)                                                    NULL,
    PRIMARY KEY (id),
    INDEX idx_todos_space_status (space_id, status),
    INDEX idx_todos_goal (goal_id),
    INDEX idx_todos_creator (space_id, creator_id),
    INDEX idx_todos_deadline (space_id, deadline),
    INDEX idx_todos_source (source_channel_id, source_channel_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS todo_assignees (
    id              CHAR(36)                    NOT NULL,
    todo_id         CHAR(36)                    NOT NULL,
    user_id         VARCHAR(64)                 NOT NULL,
    status          ENUM('pending','done')      NOT NULL DEFAULT 'pending',
    completed_at    DATETIME(3)                 NULL,
    created_at      DATETIME(3)                 NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_todo_user (todo_id, user_id),
    INDEX idx_assignees_user (user_id),
    INDEX idx_assignees_todo (todo_id),
    INDEX idx_assignees_status (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS todo_comments (
    id              CHAR(36)        NOT NULL,
    todo_id         CHAR(36)        NOT NULL,
    user_id         VARCHAR(64)     NOT NULL,
    content         TEXT            NOT NULL,
    created_at      DATETIME(3)     NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_comments_todo (todo_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS todo_attachments (
    id              CHAR(36)        NOT NULL,
    todo_id         CHAR(36)        NOT NULL,
    user_id         VARCHAR(64)     NOT NULL,
    file_url        VARCHAR(1024)   NOT NULL,
    file_name       VARCHAR(255)    NULL,
    file_size       BIGINT          NULL,
    mime_type       VARCHAR(100)    NULL,
    created_at      DATETIME(3)     NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_attachments_todo (todo_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
