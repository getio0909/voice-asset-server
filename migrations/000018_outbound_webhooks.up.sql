CREATE TABLE webhook_endpoints (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 100),
    endpoint_url text NOT NULL CHECK (
        length(endpoint_url) BETWEEN 1 AND 2048
        AND endpoint_url LIKE 'https://%'
        AND position('?' IN endpoint_url) = 0
        AND position('#' IN endpoint_url) = 0
    ),
    event_types text[] NOT NULL CHECK (
        cardinality(event_types) BETWEEN 1 AND 3
        AND event_types <@ ARRAY[
            'job.succeeded', 'job.failed', 'job.cancelled'
        ]::text[]
    ),
    state text NOT NULL CHECK (state IN ('enabled', 'disabled')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    secret_version bigint NOT NULL DEFAULT 1 CHECK (secret_version > 0),
    secret_ciphertext bytea NOT NULL CHECK (octet_length(secret_ciphertext) > 0),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (id, workspace_id),
    FOREIGN KEY (workspace_id, created_by)
        REFERENCES memberships(workspace_id, user_id)
);

CREATE INDEX webhook_endpoints_workspace_idx
    ON webhook_endpoints (workspace_id, state, lower(display_name), id);

CREATE TABLE webhook_deliveries (
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE NOT NULL,
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    webhook_id uuid NOT NULL,
    webhook_version bigint NOT NULL CHECK (webhook_version > 0),
    notification_id uuid REFERENCES notifications(id) ON DELETE SET NULL,
    event_id uuid NOT NULL,
    event_type text NOT NULL CHECK (
        event_type IN (
            'job.succeeded', 'job.failed', 'job.cancelled', 'webhook.test'
        )
    ),
    payload jsonb NOT NULL CHECK (
        jsonb_typeof(payload) = 'object'
        AND octet_length(payload::text) BETWEEN 2 AND 16384
    ),
    state text NOT NULL DEFAULT 'pending' CHECK (
        state IN (
            'pending', 'delivering', 'retry_wait',
            'succeeded', 'failed', 'cancelled'
        )
    ),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts = 5),
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_owner text,
    lease_expires_at timestamptz,
    response_status integer CHECK (
        response_status IS NULL OR response_status BETWEEN 100 AND 599
    ),
    last_error_code text CHECK (
        last_error_code IS NULL OR last_error_code IN (
            'timeout', 'transport', 'unsafe_endpoint', 'configuration',
            'http_client_error', 'http_server_error', 'response_too_large'
        )
    ),
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (webhook_id, workspace_id)
        REFERENCES webhook_endpoints(id, workspace_id) ON DELETE CASCADE,
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((state = 'delivering') = (lease_owner IS NOT NULL)),
    CHECK (attempts <= max_attempts),
    CHECK ((state = 'succeeded') = (delivered_at IS NOT NULL)),
    CHECK (state NOT IN ('succeeded', 'failed', 'cancelled') OR lease_owner IS NULL)
);

CREATE UNIQUE INDEX webhook_deliveries_notification_idx
    ON webhook_deliveries (webhook_id, notification_id)
    WHERE notification_id IS NOT NULL;

CREATE INDEX webhook_deliveries_claim_idx
    ON webhook_deliveries (available_at, sequence)
    WHERE state IN ('pending', 'retry_wait', 'delivering');

CREATE INDEX webhook_deliveries_workspace_time_idx
    ON webhook_deliveries (workspace_id, created_at DESC, id DESC);

CREATE FUNCTION guard_webhook_delivery_projection() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.webhook_id IS DISTINCT FROM OLD.webhook_id
       OR NEW.webhook_version IS DISTINCT FROM OLD.webhook_version
       OR NEW.notification_id IS DISTINCT FROM OLD.notification_id
       OR NEW.event_id IS DISTINCT FROM OLD.event_id
       OR NEW.event_type IS DISTINCT FROM OLD.event_type
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.max_attempts IS DISTINCT FROM OLD.max_attempts
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'webhook delivery projection is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER webhook_delivery_projection_guard
BEFORE UPDATE ON webhook_deliveries
FOR EACH ROW EXECUTE FUNCTION guard_webhook_delivery_projection();

CREATE FUNCTION enqueue_notification_webhooks() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO webhook_deliveries (
        workspace_id, webhook_id, webhook_version,
        notification_id, event_id, event_type, payload,
        available_at, created_at, updated_at
    )
    SELECT
        NEW.workspace_id,
        endpoint.id,
        endpoint.version,
        NEW.id,
        NEW.id,
        NEW.type,
        jsonb_build_object(
            'id', NEW.id,
            'type', NEW.type,
            'created_at', NEW.occurred_at,
            'data', jsonb_build_object(
                'job', jsonb_strip_nulls(jsonb_build_object(
                    'id', NEW.job_id,
                    'kind', NEW.job_kind,
                    'state', NEW.state,
                    'asset_id', NEW.asset_id,
                    'result_revision_id', NEW.result_revision_id,
                    'error_code', NEW.error_code
                ))
            )
        ),
        NEW.occurred_at,
        NEW.occurred_at,
        NEW.occurred_at
    FROM webhook_endpoints AS endpoint
    WHERE endpoint.workspace_id = NEW.workspace_id
      AND endpoint.state = 'enabled'
      AND NEW.type = ANY(endpoint.event_types);
    RETURN NEW;
END;
$$;

CREATE TRIGGER notifications_enqueue_webhooks
AFTER INSERT ON notifications
FOR EACH ROW EXECUTE FUNCTION enqueue_notification_webhooks();

CREATE FUNCTION cancel_stale_webhook_deliveries() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.version IS DISTINCT FROM OLD.version THEN
        UPDATE webhook_deliveries
        SET state = 'cancelled',
            lease_owner = NULL,
            lease_expires_at = NULL,
            updated_at = clock_timestamp()
        WHERE webhook_id = OLD.id
          AND webhook_version = OLD.version
          AND state IN ('pending', 'retry_wait');
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER webhook_endpoint_cancel_stale_deliveries
AFTER UPDATE OF version ON webhook_endpoints
FOR EACH ROW EXECUTE FUNCTION cancel_stale_webhook_deliveries();
