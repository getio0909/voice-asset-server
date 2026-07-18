CREATE TABLE audio_clips (
    id uuid PRIMARY KEY REFERENCES asset_objects(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    asset_id uuid NOT NULL,
    start_ms bigint NOT NULL CHECK (start_ms >= 0),
    end_ms bigint NOT NULL CHECK (end_ms > start_ms),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, asset_id)
        REFERENCES assets(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id)
);
CREATE INDEX audio_clips_workspace_asset_created_idx
    ON audio_clips (workspace_id, asset_id, created_at DESC);
CREATE INDEX audio_clips_expiry_idx ON audio_clips (expires_at);

CREATE TABLE transcript_exports (
    id uuid PRIMARY KEY REFERENCES asset_objects(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    asset_id uuid NOT NULL,
    revision_id uuid NOT NULL REFERENCES transcript_revisions(id),
    format text NOT NULL CHECK (format IN ('json', 'markdown', 'srt', 'vtt')),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, asset_id)
        REFERENCES assets(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id)
);
CREATE INDEX transcript_exports_workspace_revision_created_idx
    ON transcript_exports (workspace_id, revision_id, created_at DESC);
CREATE INDEX transcript_exports_expiry_idx ON transcript_exports (expires_at);
