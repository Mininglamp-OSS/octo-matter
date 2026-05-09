-- 002_rename_to_matters.up.sql
-- NOTE: Users who previously had access via Goal assignee relationship will
-- lose visibility after this migration. Ensure all relevant matters have
-- direct assignees added before running.


-- Remove goal_id FK/index/column from todos BEFORE dropping goals table
ALTER TABLE todos DROP FOREIGN KEY fk_todos_goal;
ALTER TABLE todos DROP INDEX idx_todos_goal;
ALTER TABLE todos DROP COLUMN goal_id;

-- Drop goal tables (now safe: no FK references them)
DROP TABLE IF EXISTS goal_assignees;
DROP TABLE IF EXISTS goals;

-- Step 1: Widen enum to superset (both old and new values valid)
ALTER TABLE todos MODIFY status ENUM('open','closed','done','archived') NOT NULL DEFAULT 'open';

-- Step 2: Backfill data (now 'done' is a valid enum value)
UPDATE todos SET status = 'done' WHERE status = 'closed';

-- Step 3: Narrow enum to final shape (no rows have 'closed' anymore)
ALTER TABLE todos MODIFY status ENUM('open','done','archived') NOT NULL DEFAULT 'open';

-- Rename tables
RENAME TABLE todos TO matters;
RENAME TABLE todo_assignees TO matter_assignees;
RENAME TABLE todo_comments TO matter_comments;
RENAME TABLE todo_comment_attachments TO matter_comment_attachments;

-- Fix foreign keys and column names
ALTER TABLE matter_assignees DROP FOREIGN KEY fk_todo_assignees_todo;
ALTER TABLE matter_assignees CHANGE todo_id matter_id CHAR(36) NOT NULL;
ALTER TABLE matter_assignees ADD CONSTRAINT fk_matter_assignees_matter FOREIGN KEY (matter_id) REFERENCES matters(id) ON DELETE CASCADE;

ALTER TABLE matter_comments DROP FOREIGN KEY fk_todo_comments_todo;
ALTER TABLE matter_comments CHANGE todo_id matter_id CHAR(36) NOT NULL;
ALTER TABLE matter_comments ADD CONSTRAINT fk_matter_comments_matter FOREIGN KEY (matter_id) REFERENCES matters(id) ON DELETE CASCADE;

ALTER TABLE matter_comment_attachments DROP FOREIGN KEY fk_comment_attachments_comment;
ALTER TABLE matter_comment_attachments ADD CONSTRAINT fk_matter_comment_attachments_comment FOREIGN KEY (comment_id) REFERENCES matter_comments(id) ON DELETE CASCADE;

-- Fix index names
ALTER TABLE matter_assignees DROP INDEX uk_todo_user;
ALTER TABLE matter_assignees ADD UNIQUE KEY uk_matter_user (matter_id, user_id);
ALTER TABLE matter_assignees DROP INDEX idx_assignees_todo;
ALTER TABLE matter_assignees ADD INDEX idx_assignees_matter (matter_id);
