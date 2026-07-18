CREATE INDEX provider_profiles_enabled_llm_route_idx
    ON provider_profiles (workspace_id, priority, id)
    WHERE provider_type = 'llm' AND state = 'enabled';

CREATE TABLE glossary_sets (
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
CREATE UNIQUE INDEX glossary_sets_workspace_name_unique
    ON glossary_sets (workspace_id, lower(display_name));
CREATE INDEX glossary_sets_scope_idx
    ON glossary_sets (workspace_id, scope_type, scope_id)
    WHERE state = 'enabled';

CREATE TABLE glossary_set_versions (
    id uuid PRIMARY KEY,
    glossary_set_id uuid NOT NULL REFERENCES glossary_sets(id),
    version integer NOT NULL CHECK (version > 0),
    entries jsonb NOT NULL CHECK (
        jsonb_typeof(entries) = 'array'
        AND jsonb_array_length(entries) BETWEEN 1 AND 500
    ),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (glossary_set_id, version)
);

ALTER TABLE transcript_revisions
    ADD COLUMN created_by_type text NOT NULL DEFAULT 'system'
        CHECK (created_by_type IN ('user', 'agent', 'system')),
    ADD COLUMN model text,
    ADD COLUMN prompt_version text,
    ADD COLUMN review_status text NOT NULL DEFAULT 'pending'
        CHECK (review_status IN ('pending', 'reviewed', 'approved', 'rejected'));

CREATE UNIQUE INDEX transcript_revisions_correction_job_unique
    ON transcript_revisions (source_job_id)
    WHERE kind = 'llm_corrected' AND source_job_id IS NOT NULL;

CREATE TABLE transcript_revision_reviews (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    revision_id uuid NOT NULL REFERENCES transcript_revisions(id),
    reviewer_id uuid NOT NULL,
    action text NOT NULL CHECK (action IN (
        'accept_change', 'reject_change', 'accept_all', 'reject_all', 'approve'
    )),
    change_index integer CHECK (change_index IS NULL OR change_index >= 0),
    resulting_revision_id uuid REFERENCES transcript_revisions(id),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (workspace_id, reviewer_id)
        REFERENCES memberships(workspace_id, user_id),
    CHECK (
        (action IN ('accept_change', 'reject_change')) = (change_index IS NOT NULL)
    )
);
CREATE INDEX transcript_revision_reviews_revision_idx
    ON transcript_revision_reviews (revision_id, created_at, id);
CREATE UNIQUE INDEX transcript_revision_reviews_one_approval
    ON transcript_revision_reviews (revision_id)
    WHERE action = 'approve';

CREATE FUNCTION reject_transcript_review_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable transcript review % cannot be changed', OLD.id;
END;
$$;

CREATE TRIGGER transcript_review_immutable_guard
BEFORE UPDATE OR DELETE ON transcript_revision_reviews
FOR EACH ROW EXECUTE FUNCTION reject_transcript_review_mutation();
