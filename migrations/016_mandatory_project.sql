-- 016_mandatory_project.sql
-- Every matter must belong to a project. Create a default "收件箱" project
-- per space for orphans, then migrate them.
-- NOTE: The actual migration is run by the Go service on startup via
-- GetOrCreateDefault; this file documents the intent.

-- For each space, create a default project if not exists:
-- INSERT INTO matter_projects (id, space_id, name, scope, creator_id, ...)
-- VALUES (UUID(), '<space>', '收件箱', 'default', '<first_creator>', ...);

-- Then assign orphans:
-- UPDATE matters SET project_id = <default_project_id>
-- WHERE project_id IS NULL AND space_id = '<space>';

-- +migrate Up
SELECT 1;

-- +migrate Down
SELECT 1;
