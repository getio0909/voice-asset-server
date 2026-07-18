ALTER TABLE provider_profiles
    ADD CONSTRAINT provider_profiles_id_workspace_unique UNIQUE (id, workspace_id);

CREATE TABLE recording_sessions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    created_by uuid NOT NULL,
    client_session_id uuid NOT NULL,
    provider_profile_id uuid,
    hotword_set_id uuid,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    idempotency_request_hash char(64) NOT NULL
        CHECK (idempotency_request_hash ~ '^[0-9a-f]{64}$'),
    protocol_version text NOT NULL CHECK (protocol_version = '1'),
    audio_encoding text NOT NULL CHECK (audio_encoding = 'pcm_s16le'),
    sample_rate_hz integer NOT NULL CHECK (sample_rate_hz IN (8000, 16000, 24000, 48000)),
    channels smallint NOT NULL CHECK (channels = 1),
    frame_duration_ms integer NOT NULL CHECK (frame_duration_ms BETWEEN 20 AND 100),
    language text NOT NULL CHECK (length(language) BETWEEN 2 AND 35),
    state text NOT NULL CHECK (state IN (
        'streaming', 'interrupted', 'finalizing', 'completed', 'failed', 'expired'
    )),
    next_sequence bigint NOT NULL DEFAULT 0 CHECK (next_sequence >= 0),
    received_bytes bigint NOT NULL DEFAULT 0 CHECK (received_bytes >= 0),
    last_captured_at_ms bigint CHECK (last_captured_at_ms IS NULL OR last_captured_at_ms >= 0),
    final_transcript text CHECK (
        final_transcript IS NULL OR octet_length(final_transcript) BETWEEN 1 AND 262144
    ),
    final_language text CHECK (
        final_language IS NULL OR (
            length(final_language) BETWEEN 2 AND 35
            AND final_language ~ '^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$'
        )
    ),
    final_provider_id text CHECK (
        final_provider_id IS NULL OR final_provider_id ~ '^[a-z0-9][a-z0-9_-]{0,63}$'
    ),
    client_archive_sha256 char(64) CHECK (
        client_archive_sha256 IS NULL OR client_archive_sha256 ~ '^[0-9a-f]{64}$'
    ),
    captured_duration_ms bigint CHECK (captured_duration_ms IS NULL OR captured_duration_ms >= 0),
    last_error_code text CHECK (last_error_code IS NULL OR length(last_error_code) BETWEEN 1 AND 64),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    started_at timestamptz NOT NULL,
    last_frame_at timestamptz,
    interrupted_at timestamptz,
    reconnect_by timestamptz,
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id),
    FOREIGN KEY (provider_profile_id, workspace_id)
        REFERENCES provider_profiles(id, workspace_id),
    FOREIGN KEY (hotword_set_id, workspace_id)
        REFERENCES hotword_sets(id, workspace_id),
    UNIQUE (workspace_id, idempotency_key),
    UNIQUE (workspace_id, client_session_id),
    CHECK (expires_at > started_at),
    CHECK ((next_sequence = 0) = (last_frame_at IS NULL)),
    CHECK ((next_sequence = 0) = (last_captured_at_ms IS NULL)),
    CHECK (
        (state = 'interrupted' AND interrupted_at IS NOT NULL AND reconnect_by IS NOT NULL)
        OR (state <> 'interrupted' AND interrupted_at IS NULL AND reconnect_by IS NULL)
    ),
    CHECK ((client_archive_sha256 IS NULL) = (captured_duration_ms IS NULL)),
    CHECK (
        state NOT IN ('finalizing', 'completed')
        OR client_archive_sha256 IS NOT NULL
    ),
    CHECK (
        (state = 'completed' AND final_transcript IS NOT NULL
            AND final_language IS NOT NULL AND final_provider_id IS NOT NULL
            AND completed_at IS NOT NULL)
        OR (state <> 'completed' AND final_transcript IS NULL
            AND final_language IS NULL AND final_provider_id IS NULL
            AND completed_at IS NULL)
    ),
    CHECK ((state = 'failed') = (last_error_code IS NOT NULL))
);

CREATE INDEX recording_sessions_workspace_state_idx
    ON recording_sessions (workspace_id, state, updated_at DESC, id DESC);
CREATE INDEX recording_sessions_expiry_idx
    ON recording_sessions (expires_at, id)
    WHERE state IN ('streaming', 'interrupted', 'finalizing');
