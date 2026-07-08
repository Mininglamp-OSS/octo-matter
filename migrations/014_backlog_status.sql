-- 014_backlog_status.sql
-- Add "backlog" to the matter status enum (pre-open staging state).

-- +migrate Up
ALTER TABLE matters
    MODIFY COLUMN status ENUM('backlog','open','in_progress','review','done','blocked','cancelled','archived')
    NOT NULL DEFAULT 'open';

-- +migrate Down
ALTER TABLE matters
    MODIFY COLUMN status ENUM('open','in_progress','review','done','blocked','cancelled','archived')
    NOT NULL DEFAULT 'open';
