DROP INDEX IF EXISTS memberships_active_user_idx;
DROP INDEX IF EXISTS memberships_workspace_updated_idx;

ALTER TABLE memberships
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS status;
