ALTER TABLE assets
    ADD CONSTRAINT assets_workspace_id_unique UNIQUE (workspace_id, id);

CREATE TABLE collections (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id)
);
CREATE UNIQUE INDEX collections_workspace_name_unique
    ON collections (workspace_id, lower(name));
CREATE INDEX collections_workspace_created_idx
    ON collections (workspace_id, created_at DESC, id DESC);

ALTER TABLE assets ADD COLUMN collection_id uuid;
ALTER TABLE assets
    ADD CONSTRAINT assets_collection_workspace_fk
    FOREIGN KEY (workspace_id, collection_id)
    REFERENCES collections(workspace_id, id);
CREATE INDEX assets_workspace_collection_created_idx
    ON assets (workspace_id, collection_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE tags (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    color text CHECK (color IS NULL OR color ~ '^#[0-9A-Fa-f]{6}$'),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id)
);
CREATE UNIQUE INDEX tags_workspace_name_unique
    ON tags (workspace_id, lower(name));
CREATE INDEX tags_workspace_created_idx
    ON tags (workspace_id, created_at DESC, id DESC);

CREATE TABLE asset_tags (
    workspace_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    tag_id uuid NOT NULL,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, asset_id, tag_id),
    FOREIGN KEY (workspace_id, asset_id)
        REFERENCES assets(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, tag_id)
        REFERENCES tags(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id)
);
CREATE INDEX asset_tags_workspace_tag_asset_idx
    ON asset_tags (workspace_id, tag_id, asset_id);

CREATE TABLE annotations (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('bookmark', 'note')),
    start_ms bigint NOT NULL CHECK (start_ms >= 0),
    end_ms bigint CHECK (end_ms IS NULL OR end_ms > start_ms),
    body text NOT NULL DEFAULT '' CHECK (length(body) <= 4000),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, asset_id)
        REFERENCES assets(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id)
);
CREATE INDEX annotations_workspace_asset_created_idx
    ON annotations (workspace_id, asset_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
