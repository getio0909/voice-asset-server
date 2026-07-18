CREATE TABLE notifications (
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE NOT NULL,
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    recipient_user_id uuid NOT NULL,
    type text NOT NULL CHECK (type IN ('job.succeeded', 'job.failed', 'job.cancelled')),
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    job_kind text NOT NULL CHECK (length(job_kind) BETWEEN 1 AND 100),
    state text NOT NULL CHECK (state IN ('succeeded', 'failed', 'cancelled')),
    asset_id uuid,
    result_revision_id uuid,
    error_code text CHECK (
        error_code IS NULL OR error_code IN (
            'internal_error', 'provider_unavailable', 'invalid_audio',
            'provider_rejected', 'worker_timeout', 'lease_expired'
        )
    ),
    occurred_at timestamptz NOT NULL,
    FOREIGN KEY (workspace_id, recipient_user_id)
        REFERENCES memberships(workspace_id, user_id) ON DELETE CASCADE,
    CHECK (type = 'job.' || state)
);

CREATE INDEX notifications_recipient_sequence_idx
    ON notifications (workspace_id, recipient_user_id, sequence);

CREATE FUNCTION reject_notification_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'notifications are immutable';
END;
$$;

CREATE TRIGGER notification_immutable_guard
BEFORE UPDATE ON notifications
FOR EACH ROW EXECUTE FUNCTION reject_notification_update();

INSERT INTO notifications (
    workspace_id, recipient_user_id, type, job_id, job_kind, state,
    asset_id, result_revision_id, error_code, occurred_at
)
SELECT
    workspace_id, created_by, 'job.' || state, id, kind, state,
    asset_id, result_revision_id, last_error_code, updated_at
FROM jobs
WHERE state IN ('succeeded', 'failed', 'cancelled')
ORDER BY updated_at, id;

CREATE FUNCTION append_terminal_job_notification() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state IN ('succeeded', 'failed', 'cancelled')
       AND (TG_OP = 'INSERT' OR OLD.state IS DISTINCT FROM NEW.state) THEN
        INSERT INTO notifications (
            workspace_id, recipient_user_id, type, job_id, job_kind, state,
            asset_id, result_revision_id, error_code, occurred_at
        ) VALUES (
            NEW.workspace_id, NEW.created_by, 'job.' || NEW.state,
            NEW.id, NEW.kind, NEW.state, NEW.asset_id,
            NEW.result_revision_id, NEW.last_error_code, NEW.updated_at
        );
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER jobs_append_terminal_notification
AFTER INSERT OR UPDATE OF state ON jobs
FOR EACH ROW EXECUTE FUNCTION append_terminal_job_notification();
