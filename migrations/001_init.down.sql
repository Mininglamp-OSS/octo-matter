-- 001_init.down.sql: Drop all tables (reverse FK order)

DROP TABLE IF EXISTS todo_comment_attachments;
DROP TABLE IF EXISTS todo_comments;
DROP TABLE IF EXISTS todo_assignees;
DROP TABLE IF EXISTS todos;
DROP TABLE IF EXISTS goal_assignees;
DROP TABLE IF EXISTS goals;
