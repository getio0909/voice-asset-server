CREATE TABLE api_keys (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    created_by uuid NOT NULL,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    token_prefix text NOT NULL CHECK (token_prefix ~ '^va_pat_[A-Za-z0-9_-]{8}$'),
    token_hash char(64) NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    scopes text[] NOT NULL CHECK (
        cardinality(scopes) BETWEEN 1 AND 20
        AND scopes <@ ARRAY[
            'admin:read', 'admin:write', 'assets:read', 'assets:write',
            'audio:read', 'corrections:write', 'metadata:write',
            'transcriptions:write', 'transcripts:read'
        ]::text[]
    ),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (id, workspace_id),
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id),
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CHECK (last_used_at IS NULL OR last_used_at >= created_at)
);

CREATE INDEX api_keys_workspace_created_idx
    ON api_keys (workspace_id, created_at DESC, id DESC);
CREATE INDEX api_keys_active_token_idx
    ON api_keys (token_hash, expires_at)
    WHERE revoked_at IS NULL;
