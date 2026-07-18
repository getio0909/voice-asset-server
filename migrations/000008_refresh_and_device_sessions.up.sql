ALTER TABLE sessions
    ADD COLUMN refresh_token_hash char(64),
    ADD COLUMN refresh_expires_at timestamptz,
    ADD COLUMN device_name text NOT NULL DEFAULT 'Legacy session',
    ADD COLUMN last_seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN rotated_at timestamptz,
    ADD CONSTRAINT sessions_refresh_pair_check CHECK (
        (refresh_token_hash IS NULL) = (refresh_expires_at IS NULL)
    ),
    ADD CONSTRAINT sessions_refresh_hash_check CHECK (
        refresh_token_hash IS NULL OR refresh_token_hash ~ '^[0-9a-f]{64}$'
    ),
    ADD CONSTRAINT sessions_refresh_expiry_check CHECK (
        refresh_expires_at IS NULL OR refresh_expires_at > created_at
    ),
    ADD CONSTRAINT sessions_device_name_check CHECK (
        char_length(device_name) BETWEEN 1 AND 100
    ),
    ADD CONSTRAINT sessions_last_seen_check CHECK (last_seen_at >= created_at),
    ADD CONSTRAINT sessions_rotated_check CHECK (
        rotated_at IS NULL OR rotated_at >= created_at
    );

CREATE UNIQUE INDEX sessions_refresh_token_unique
    ON sessions (refresh_token_hash)
    WHERE refresh_token_hash IS NOT NULL;
CREATE INDEX sessions_user_device_active_idx
    ON sessions (workspace_id, user_id, last_seen_at DESC, id)
    WHERE revoked_at IS NULL;
CREATE INDEX sessions_active_refresh_idx
    ON sessions (refresh_token_hash, refresh_expires_at)
    WHERE revoked_at IS NULL AND refresh_token_hash IS NOT NULL;
