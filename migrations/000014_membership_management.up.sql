ALTER TABLE memberships
    ADD COLUMN status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled')),
    ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT clock_timestamp();

CREATE INDEX memberships_workspace_updated_idx
    ON memberships (workspace_id, updated_at DESC, user_id DESC);
CREATE INDEX memberships_active_user_idx
    ON memberships (user_id, workspace_id)
    WHERE status = 'active';
