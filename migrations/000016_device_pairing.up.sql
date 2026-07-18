CREATE TABLE pairing_sessions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret_hash char(64) NOT NULL UNIQUE
        CHECK (secret_hash ~ '^[0-9a-f]{64}$'),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    revoked_at timestamptz,
    claimed_session_id uuid UNIQUE REFERENCES sessions(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (expires_at > created_at),
    CHECK (claimed_session_id IS NULL OR claimed_at IS NOT NULL),
    CHECK (claimed_at IS NULL OR claimed_at < expires_at),
    CHECK (claimed_at IS NULL OR revoked_at IS NULL),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX pairing_sessions_user_active_idx
    ON pairing_sessions (workspace_id, user_id, created_at DESC, id)
    WHERE claimed_at IS NULL AND revoked_at IS NULL;

CREATE INDEX pairing_sessions_expiry_idx
    ON pairing_sessions (expires_at, id)
    WHERE claimed_at IS NULL AND revoked_at IS NULL;
