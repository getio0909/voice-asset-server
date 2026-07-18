CREATE TABLE sync_changes (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    entity_type text NOT NULL CHECK (entity_type = 'asset'),
    entity_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('upsert', 'delete')),
    entity_version bigint NOT NULL CHECK (entity_version > 0),
    payload jsonb,
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT sync_changes_payload_check CHECK (
        (operation = 'upsert' AND jsonb_typeof(payload) = 'object') OR
        (operation = 'delete' AND payload IS NULL)
    )
);

CREATE INDEX sync_changes_workspace_sequence_idx
    ON sync_changes (workspace_id, sequence);

CREATE FUNCTION append_asset_sync_change() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        INSERT INTO sync_changes (
            workspace_id, entity_type, entity_id, operation, entity_version, payload, changed_at
        ) VALUES (
            OLD.workspace_id, 'asset', OLD.id, 'delete', OLD.version, NULL, clock_timestamp()
        );
        RETURN OLD;
    END IF;

    INSERT INTO sync_changes (
        workspace_id, entity_type, entity_id, operation, entity_version, payload, changed_at
    ) VALUES (
        NEW.workspace_id,
        'asset',
        NEW.id,
        'upsert',
        NEW.version,
        jsonb_build_object(
            'id', NEW.id::text,
            'collection_id', CASE WHEN NEW.collection_id IS NULL THEN NULL ELSE to_jsonb(NEW.collection_id::text) END,
            'title', NEW.title,
            'language', NEW.language,
            'status', NEW.status,
            'duration_ms', NEW.duration_ms,
            'version', NEW.version,
            'created_at', to_char(NEW.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
            'updated_at', to_char(NEW.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
            'trashed_at', CASE
                WHEN NEW.deleted_at IS NULL THEN NULL
                ELSE to_jsonb(to_char(NEW.deleted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
            END
        ),
        clock_timestamp()
    );
    RETURN NEW;
END;
$$;

INSERT INTO sync_changes (
    workspace_id, entity_type, entity_id, operation, entity_version, payload, changed_at
)
SELECT
    asset.workspace_id,
    'asset',
    asset.id,
    'upsert',
    asset.version,
    jsonb_build_object(
        'id', asset.id::text,
        'collection_id', CASE WHEN asset.collection_id IS NULL THEN NULL ELSE to_jsonb(asset.collection_id::text) END,
        'title', asset.title,
        'language', asset.language,
        'status', asset.status,
        'duration_ms', asset.duration_ms,
        'version', asset.version,
        'created_at', to_char(asset.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        'updated_at', to_char(asset.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
        'trashed_at', CASE
            WHEN asset.deleted_at IS NULL THEN NULL
            ELSE to_jsonb(to_char(asset.deleted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
        END
    ),
    asset.updated_at
FROM assets asset
ORDER BY asset.created_at, asset.id;

CREATE TRIGGER assets_append_sync_change
AFTER INSERT OR UPDATE OR DELETE ON assets
FOR EACH ROW EXECUTE FUNCTION append_asset_sync_change();
