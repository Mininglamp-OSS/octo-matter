-- 001_init.up.sql: Create all tables for Octo Todo (v1 MVP)

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

CREATE TABLE IF NOT EXISTS todos (
    id                   CHAR(36)                                                       NOT NULL,
    space_id             VARCHAR(64)                                                    NOT NULL,
    goal_id              CHAR(36)                                                       NULL,
    title                VARCHAR(500)                                                   NOT NULL,
    description          TEXT                                                           NULL,
    creator_id           VARCHAR(64)                                                    NOT NULL,
    status               ENUM('open','closed')                                          NOT NULL DEFAULT 'open',
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
    INDEX idx_todos_source (source_channel_id, source_channel_type),
    CONSTRAINT fk_todos_goal FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS todo_assignees (
    id              CHAR(36)                    NOT NULL,
    todo_id         CHAR(36)                    NOT NULL,
    user_id         VARCHAR(64)                 NOT NULL,
    created_at      DATETIME(3)                 NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_todo_user (todo_id, user_id),
    INDEX idx_assignees_user (user_id),
    INDEX idx_assignees_todo (todo_id),
    CONSTRAINT fk_todo_assignees_todo FOREIGN KEY (todo_id) REFERENCES todos(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS todo_comments (
    id              CHAR(36)        NOT NULL,
    todo_id         CHAR(36)        NOT NULL,
    user_id         VARCHAR(64)     NOT NULL,
    content         TEXT            NOT NULL,
    created_at      DATETIME(3)     NOT NULL,
    PRIMARY KEY (id),
    INDEX idx_comments_todo (todo_id),
    CONSTRAINT fk_todo_comments_todo FOREIGN KEY (todo_id) REFERENCES todos(id) ON DELETE CASCADE
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
    INDEX idx_attachments_todo (todo_id),
    CONSTRAINT fk_todo_attachments_todo FOREIGN KEY (todo_id) REFERENCES todos(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
