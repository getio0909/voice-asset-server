ALTER TABLE assets
    ADD COLUMN status_before_trash text
    CHECK (
        status_before_trash IS NULL
        OR status_before_trash IN ('draft', 'uploading', 'processing', 'ready', 'failed')
    );

UPDATE assets
SET status_before_trash = 'draft'
WHERE status = 'trashed' AND deleted_at IS NOT NULL AND status_before_trash IS NULL;

CREATE INDEX assets_workspace_status_created_idx
    ON assets (workspace_id, status, created_at DESC, id DESC);

CREATE INDEX assets_workspace_deleted_created_idx
    ON assets (workspace_id, deleted_at, created_at DESC, id DESC)
    WHERE deleted_at IS NOT NULL;
