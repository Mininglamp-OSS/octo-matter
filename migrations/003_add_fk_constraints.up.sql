ALTER TABLE goal_assignees ADD CONSTRAINT fk_goal_assignees_goal FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE CASCADE;
ALTER TABLE todos ADD CONSTRAINT fk_todos_goal FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE SET NULL;
ALTER TABLE todo_assignees ADD CONSTRAINT fk_todo_assignees_todo FOREIGN KEY (todo_id) REFERENCES todos(id) ON DELETE CASCADE;
ALTER TABLE todo_comments ADD CONSTRAINT fk_todo_comments_todo FOREIGN KEY (todo_id) REFERENCES todos(id) ON DELETE CASCADE;
ALTER TABLE todo_attachments ADD CONSTRAINT fk_todo_attachments_todo FOREIGN KEY (todo_id) REFERENCES todos(id) ON DELETE CASCADE;
