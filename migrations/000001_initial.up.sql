CREATE TABLE workspaces (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL CHECK (position('@' IN email) > 1),
    password_hash text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'disabled', 'locked')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX users_email_unique ON users (lower(email));

CREATE TABLE memberships (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'viewer', 'agent')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE assets (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    status text NOT NULL CHECK (status IN ('draft', 'uploading', 'processing', 'ready', 'failed', 'trashed')),
    duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz
);
CREATE INDEX assets_workspace_created_idx ON assets (workspace_id, created_at DESC);

CREATE TABLE asset_objects (
    id uuid PRIMARY KEY,
    asset_id uuid NOT NULL REFERENCES assets(id),
    parent_object_id uuid REFERENCES asset_objects(id),
    kind text NOT NULL CHECK (kind IN ('original', 'playback', 'asr_input', 'waveform', 'clip', 'provider_raw_response', 'export')),
    storage_backend text NOT NULL CHECK (storage_backend IN ('local', 's3')),
    storage_key text NOT NULL,
    mime_type text NOT NULL,
    container text,
    codec text,
    sample_rate integer CHECK (sample_rate IS NULL OR sample_rate > 0),
    channel_count smallint CHECK (channel_count IS NULL OR channel_count > 0),
    bitrate bigint CHECK (bitrate IS NULL OR bitrate >= 0),
    duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0),
    file_size bigint NOT NULL CHECK (file_size >= 0),
    sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    creation_source text NOT NULL,
    encryption_state text NOT NULL CHECK (encryption_state IN ('none', 'server_managed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (storage_backend, storage_key)
);
CREATE INDEX asset_objects_asset_kind_idx ON asset_objects (asset_id, kind);

CREATE TABLE transcripts (
    id uuid PRIMARY KEY,
    asset_id uuid NOT NULL REFERENCES assets(id),
    language text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (id, asset_id)
);

CREATE TABLE transcript_revisions (
    id uuid PRIMARY KEY,
    transcript_id uuid NOT NULL REFERENCES transcripts(id),
    parent_revision_id uuid REFERENCES transcript_revisions(id),
    kind text NOT NULL CHECK (kind IN ('raw_asr', 'normalized', 'llm_corrected', 'human_edited', 'approved')),
    text_content text NOT NULL,
    provider_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    hotword_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    glossary_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    diff jsonb NOT NULL DEFAULT '{}'::jsonb,
    validation_result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX transcript_revisions_transcript_created_idx
    ON transcript_revisions (transcript_id, created_at DESC);

CREATE TABLE jobs (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    asset_id uuid REFERENCES assets(id),
    kind text NOT NULL,
    state text NOT NULL CHECK (state IN ('queued', 'running', 'retry_wait', 'succeeded', 'failed', 'cancelled')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX jobs_claim_idx ON jobs (state, available_at, created_at)
    WHERE state IN ('queued', 'retry_wait');

CREATE TABLE provider_profiles (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    provider_type text NOT NULL CHECK (provider_type IN ('asr', 'llm')),
    provider_id text NOT NULL,
    display_name text NOT NULL,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_ciphertext bytea,
    state text NOT NULL CHECK (state IN ('enabled', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE audit_logs (
    id uuid PRIMARY KEY,
    workspace_id uuid REFERENCES workspaces(id),
    actor_id uuid REFERENCES users(id),
    actor_type text NOT NULL CHECK (actor_type IN ('user', 'agent', 'system')),
    action text NOT NULL,
    target_type text NOT NULL,
    target_id uuid,
    request_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX audit_logs_workspace_time_idx ON audit_logs (workspace_id, occurred_at DESC);

CREATE TABLE system_settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE FUNCTION reject_asset_object_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.kind IN ('original', 'provider_raw_response') THEN
        RAISE EXCEPTION 'immutable asset object % cannot be changed', OLD.id;
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER asset_object_immutable_guard
BEFORE UPDATE OR DELETE ON asset_objects
FOR EACH ROW EXECUTE FUNCTION reject_asset_object_mutation();

CREATE FUNCTION reject_transcript_revision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable transcript revision % cannot be changed', OLD.id;
END;
$$;

CREATE TRIGGER transcript_revision_immutable_guard
BEFORE UPDATE OR DELETE ON transcript_revisions
FOR EACH ROW EXECUTE FUNCTION reject_transcript_revision_mutation();
