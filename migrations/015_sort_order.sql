-- 015_sort_order.sql
-- Add sort_order for user-controlled ordering in board/timeline views.

-- +migrate Up
ALTER TABLE matters ADD COLUMN sort_order DOUBLE DEFAULT NULL AFTER deadline;

-- +migrate Down
ALTER TABLE matters DROP COLUMN sort_order;
