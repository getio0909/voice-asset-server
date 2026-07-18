ALTER TABLE provider_profiles
    ADD COLUMN priority integer NOT NULL DEFAULT 100 CHECK (priority BETWEEN 1 AND 1000),
    ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    ADD COLUMN created_by uuid REFERENCES users(id),
    ADD CONSTRAINT provider_profiles_config_object_check
        CHECK (jsonb_typeof(config) = 'object'),
    ADD CONSTRAINT provider_profiles_secret_size_check
        CHECK (secret_ciphertext IS NULL OR octet_length(secret_ciphertext) BETWEEN 32 AND 131072);

CREATE UNIQUE INDEX provider_profiles_workspace_name_unique
    ON provider_profiles (workspace_id, provider_type, lower(display_name));
CREATE INDEX provider_profiles_enabled_asr_route_idx
    ON provider_profiles (workspace_id, priority, id)
    WHERE provider_type = 'asr' AND state = 'enabled';

CREATE TABLE hotword_sets (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 100),
    scope_type text NOT NULL CHECK (scope_type IN ('workspace', 'collection', 'asset')),
    scope_id uuid,
    state text NOT NULL CHECK (state IN ('enabled', 'disabled')),
    current_version integer NOT NULL DEFAULT 1 CHECK (current_version > 0),
    row_version bigint NOT NULL DEFAULT 1 CHECK (row_version > 0),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id),
    CHECK ((scope_type = 'workspace') = (scope_id IS NULL)),
    UNIQUE (id, workspace_id)
);
CREATE UNIQUE INDEX hotword_sets_workspace_name_unique
    ON hotword_sets (workspace_id, lower(display_name));
CREATE INDEX hotword_sets_scope_idx
    ON hotword_sets (workspace_id, scope_type, scope_id)
    WHERE state = 'enabled';

CREATE TABLE hotword_set_versions (
    id uuid PRIMARY KEY,
    hotword_set_id uuid NOT NULL REFERENCES hotword_sets(id),
    version integer NOT NULL CHECK (version > 0),
    entries jsonb NOT NULL CHECK (
        jsonb_typeof(entries) = 'array'
        AND jsonb_array_length(entries) BETWEEN 1 AND 500
    ),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (hotword_set_id, version)
);

CREATE TABLE provider_profile_health_checks (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile_id uuid NOT NULL REFERENCES provider_profiles(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('healthy', 'unhealthy')),
    error_class text,
    checked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (status = 'healthy' AND error_class IS NULL)
        OR (status = 'unhealthy' AND error_class IS NOT NULL)
    )
);
CREATE INDEX provider_profile_health_latest_idx
    ON provider_profile_health_checks (profile_id, checked_at DESC);
