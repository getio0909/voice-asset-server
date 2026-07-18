DROP INDEX IF EXISTS sessions_active_refresh_idx;
DROP INDEX IF EXISTS sessions_user_device_active_idx;
DROP INDEX IF EXISTS sessions_refresh_token_unique;
ALTER TABLE sessions
    DROP CONSTRAINT IF EXISTS sessions_rotated_check,
    DROP CONSTRAINT IF EXISTS sessions_last_seen_check,
    DROP CONSTRAINT IF EXISTS sessions_device_name_check,
    DROP CONSTRAINT IF EXISTS sessions_refresh_expiry_check,
    DROP CONSTRAINT IF EXISTS sessions_refresh_hash_check,
    DROP CONSTRAINT IF EXISTS sessions_refresh_pair_check,
    DROP COLUMN IF EXISTS rotated_at,
    DROP COLUMN IF EXISTS last_seen_at,
    DROP COLUMN IF EXISTS device_name,
    DROP COLUMN IF EXISTS refresh_expires_at,
    DROP COLUMN IF EXISTS refresh_token_hash;
