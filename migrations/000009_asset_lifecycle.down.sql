DROP INDEX IF EXISTS assets_workspace_deleted_created_idx;
DROP INDEX IF EXISTS assets_workspace_status_created_idx;

ALTER TABLE assets DROP COLUMN IF EXISTS status_before_trash;
