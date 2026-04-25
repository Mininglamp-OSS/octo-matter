ALTER TABLE todo_attachments DROP FOREIGN KEY fk_todo_attachments_todo;
ALTER TABLE todo_comments DROP FOREIGN KEY fk_todo_comments_todo;
ALTER TABLE todo_assignees DROP FOREIGN KEY fk_todo_assignees_todo;
ALTER TABLE todos DROP FOREIGN KEY fk_todos_goal;
ALTER TABLE goal_assignees DROP FOREIGN KEY fk_goal_assignees_goal;
