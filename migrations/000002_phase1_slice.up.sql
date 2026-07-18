CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL,
    token_hash char(64) NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (workspace_id, user_id)
        REFERENCES memberships(workspace_id, user_id) ON DELETE CASCADE,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE INDEX sessions_active_token_idx ON sessions (token_hash, expires_at)
    WHERE revoked_at IS NULL;

ALTER TABLE assets
    ADD COLUMN language text NOT NULL DEFAULT 'und'
        CHECK (language ~ '^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$' OR language = 'und'),
    ADD COLUMN idempotency_key text,
    ADD COLUMN idempotency_request_hash char(64)
        CHECK (idempotency_request_hash IS NULL OR idempotency_request_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT assets_idempotency_pair_check CHECK (
        (idempotency_key IS NULL) = (idempotency_request_hash IS NULL)
    ),
    ADD CONSTRAINT assets_id_workspace_unique UNIQUE (id, workspace_id);
CREATE UNIQUE INDEX assets_workspace_idempotency_unique
    ON assets (workspace_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX asset_objects_one_original_per_asset
    ON asset_objects (asset_id)
    WHERE kind = 'original';

CREATE TABLE upload_sessions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    asset_id uuid NOT NULL,
    created_by uuid NOT NULL,
    filename text NOT NULL CHECK (length(filename) BETWEEN 1 AND 255),
    mime_type text NOT NULL CHECK (length(mime_type) BETWEEN 1 AND 100),
    expected_size bigint NOT NULL CHECK (expected_size > 0),
    expected_sha256 char(64) NOT NULL CHECK (expected_sha256 ~ '^[0-9a-f]{64}$'),
    part_size integer NOT NULL CHECK (part_size BETWEEN 65536 AND 16777216),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    idempotency_request_hash char(64) NOT NULL
        CHECK (idempotency_request_hash ~ '^[0-9a-f]{64}$'),
    state text NOT NULL CHECK (state IN ('active', 'assembling', 'completed', 'cancelled', 'failed')),
    error_code text,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    UNIQUE (id, workspace_id),
    UNIQUE (workspace_id, idempotency_key),
    FOREIGN KEY (asset_id, workspace_id)
        REFERENCES assets(id, workspace_id),
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id),
    CHECK (expires_at > created_at),
    CHECK ((state = 'completed') = (completed_at IS NOT NULL)),
    CHECK ((state = 'failed') = (error_code IS NOT NULL))
);
CREATE INDEX upload_sessions_asset_idx ON upload_sessions (asset_id, created_at DESC);

CREATE TABLE upload_parts (
    upload_session_id uuid NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    part_number integer NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
    storage_key text NOT NULL,
    size_bytes integer NOT NULL CHECK (size_bytes > 0),
    sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (upload_session_id, part_number),
    UNIQUE (storage_key)
);

ALTER TABLE jobs
    ADD COLUMN created_by uuid NOT NULL,
    ADD COLUMN payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 20),
    ADD COLUMN idempotency_key text,
    ADD COLUMN idempotency_request_hash char(64),
    ADD COLUMN lease_owner text,
    ADD COLUMN lease_expires_at timestamptz,
    ADD COLUMN last_error_code text,
    ADD COLUMN result_revision_id uuid REFERENCES transcript_revisions(id),
    ADD CONSTRAINT jobs_created_by_membership_fk FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id),
    ADD CONSTRAINT jobs_idempotency_pair_check CHECK (
        (idempotency_key IS NULL) = (idempotency_request_hash IS NULL)
    ),
    ADD CONSTRAINT jobs_idempotency_key_check CHECK (
        idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 200
    ),
    ADD CONSTRAINT jobs_idempotency_hash_check CHECK (
        idempotency_request_hash IS NULL
        OR idempotency_request_hash ~ '^[0-9a-f]{64}$'
    ),
    ADD CONSTRAINT jobs_lease_pair_check CHECK (
        (lease_owner IS NULL) = (lease_expires_at IS NULL)
    ),
    ADD CONSTRAINT jobs_running_lease_check CHECK (
        (state = 'running') = (lease_owner IS NOT NULL)
    ),
    ADD CONSTRAINT jobs_attempt_limit_check CHECK (attempts <= max_attempts),
    ADD CONSTRAINT jobs_last_error_code_check CHECK (
        last_error_code IS NULL OR last_error_code IN (
            'internal_error', 'provider_unavailable', 'invalid_audio',
            'provider_rejected', 'worker_timeout', 'lease_expired'
        )
    ),
    ADD CONSTRAINT jobs_asset_workspace_fk FOREIGN KEY (asset_id, workspace_id)
        REFERENCES assets(id, workspace_id);
CREATE UNIQUE INDEX jobs_workspace_idempotency_unique
    ON jobs (workspace_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX jobs_asset_kind_active_unique ON jobs (asset_id, kind)
    WHERE asset_id IS NOT NULL
      AND state IN ('queued', 'running', 'retry_wait');
CREATE INDEX jobs_expired_lease_idx ON jobs (lease_expires_at, created_at)
    WHERE state = 'running';

CREATE TABLE job_attempts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    attempt integer NOT NULL CHECK (attempt > 0),
    worker_id text NOT NULL,
    outcome text CHECK (outcome IN ('succeeded', 'retry', 'failed', 'lease_expired')),
    error_code text CHECK (error_code IS NULL OR error_code IN (
        'internal_error', 'provider_unavailable', 'invalid_audio',
        'provider_rejected', 'worker_timeout', 'lease_expired'
    )),
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    finished_at timestamptz,
    UNIQUE (job_id, attempt),
    CHECK ((outcome IS NULL) = (finished_at IS NULL)),
    CHECK (
        (outcome IS NULL AND error_code IS NULL)
        OR (outcome = 'succeeded' AND error_code IS NULL)
        OR (outcome IN ('retry', 'failed', 'lease_expired') AND error_code IS NOT NULL)
    )
);

ALTER TABLE transcript_revisions
    ADD COLUMN source_job_id uuid REFERENCES jobs(id),
    ADD COLUMN provider_raw_object_id uuid REFERENCES asset_objects(id);
CREATE UNIQUE INDEX transcript_revisions_raw_job_unique
    ON transcript_revisions (source_job_id)
    WHERE kind = 'raw_asr' AND source_job_id IS NOT NULL;

CREATE TABLE transcript_segments (
    id uuid PRIMARY KEY,
    revision_id uuid NOT NULL REFERENCES transcript_revisions(id),
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    start_ms bigint NOT NULL CHECK (start_ms >= 0),
    end_ms bigint NOT NULL CHECK (end_ms >= start_ms),
    speaker text,
    text_content text NOT NULL,
    confidence double precision CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    words jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(words) = 'array'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (revision_id, ordinal)
);

CREATE FUNCTION reject_transcript_segment_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable transcript segment % cannot be changed', OLD.id;
END;
$$;

CREATE TRIGGER transcript_segment_immutable_guard
BEFORE UPDATE OR DELETE ON transcript_segments
FOR EACH ROW EXECUTE FUNCTION reject_transcript_segment_mutation();

CREATE FUNCTION reject_audit_log_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable audit log % cannot be changed', OLD.id;
END;
$$;

CREATE TRIGGER audit_log_immutable_guard
BEFORE UPDATE OR DELETE ON audit_logs
FOR EACH ROW EXECUTE FUNCTION reject_audit_log_mutation();
