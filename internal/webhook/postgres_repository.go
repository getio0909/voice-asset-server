package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Create(
	ctx context.Context,
	params CreateParams,
) (Endpoint, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Endpoint{}, fmt.Errorf("begin webhook create: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result, err := scanEndpoint(tx.QueryRow(ctx, `
		INSERT INTO webhook_endpoints (
			id, workspace_id, display_name, endpoint_url, event_types, state,
			version, secret_version, secret_ciphertext, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, $9)
		RETURNING id::text, workspace_id::text, display_name, endpoint_url,
		          event_types, state, version, secret_ciphertext IS NOT NULL,
		          created_at, updated_at`,
		params.EndpointID, params.WorkspaceID, params.DisplayName, params.URL,
		params.EventTypes, params.State, params.Version, params.SecretCiphertext,
		params.CreatedBy,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return Endpoint{}, ErrConflict
		}
		return Endpoint{}, fmt.Errorf("insert webhook endpoint: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.CreatedBy,
		"webhook.created", "webhook", params.EndpointID, map[string]any{
			"display_name": params.DisplayName, "state": params.State,
			"event_types": params.EventTypes, "version": params.Version,
		}); err != nil {
		return Endpoint{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Endpoint{}, fmt.Errorf("commit webhook create: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) List(
	ctx context.Context,
	workspaceID string,
) ([]Endpoint, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, display_name, endpoint_url,
		       event_types, state, version, secret_ciphertext IS NOT NULL,
		       created_at, updated_at
		FROM webhook_endpoints
		WHERE workspace_id = $1
		ORDER BY lower(display_name), id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query webhooks: %w", err)
	}
	defer rows.Close()
	items := make([]Endpoint, 0)
	for rows.Next() {
		item, scanErr := scanEndpoint(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhooks: %w", err)
	}
	return items, nil
}

func (repository *PostgresRepository) ListDeliveries(
	ctx context.Context,
	workspaceID string,
	webhookID string,
	limit int,
) ([]Delivery, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, webhook_id::text, webhook_version,
		       notification_id::text, event_id::text, event_type, payload,
		       state, attempts, max_attempts, available_at, response_status,
		       last_error_code, delivered_at, created_at, updated_at
		FROM webhook_deliveries
		WHERE workspace_id = $1 AND webhook_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3`, workspaceID, webhookID, limit)
	if err != nil {
		return nil, fmt.Errorf("query webhook deliveries: %w", err)
	}
	defer rows.Close()
	items := make([]Delivery, 0)
	for rows.Next() {
		item, scanErr := scanDelivery(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhook deliveries: %w", err)
	}
	return items, nil
}

func (repository *PostgresRepository) GetStored(
	ctx context.Context,
	workspaceID,
	endpointID string,
) (StoredEndpoint, error) {
	var result StoredEndpoint
	err := repository.pool.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, display_name, endpoint_url,
		       event_types, state, version, secret_version,
		       secret_ciphertext IS NOT NULL, secret_ciphertext,
		       created_at, updated_at
		FROM webhook_endpoints
		WHERE id = $1 AND workspace_id = $2`, endpointID, workspaceID).Scan(
		&result.ID, &result.WorkspaceID, &result.DisplayName, &result.URL,
		&result.EventTypes, &result.State, &result.Version, &result.SecretVersion,
		&result.SecretConfigured, &result.SecretCiphertext,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredEndpoint{}, ErrNotFound
	}
	if err != nil {
		return StoredEndpoint{}, fmt.Errorf("query stored webhook: %w", err)
	}
	result.SecretCiphertext = append([]byte(nil), result.SecretCiphertext...)
	return result, nil
}

func (repository *PostgresRepository) Update(
	ctx context.Context,
	params UpdateParams,
) (Endpoint, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Endpoint{}, fmt.Errorf("begin webhook update: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result, err := scanEndpoint(tx.QueryRow(ctx, `
		UPDATE webhook_endpoints
		SET display_name = $3, endpoint_url = $4, event_types = $5, state = $6,
		    version = version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND version = $7
		RETURNING id::text, workspace_id::text, display_name, endpoint_url,
		          event_types, state, version, secret_ciphertext IS NOT NULL,
		          created_at, updated_at`,
		params.EndpointID, params.WorkspaceID, params.DisplayName, params.URL,
		params.EventTypes, params.State, params.ExpectedVersion,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrConflict
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("update webhook endpoint: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.UpdatedBy,
		"webhook.updated", "webhook", params.EndpointID, map[string]any{
			"state": params.State, "event_types": params.EventTypes,
			"version": result.Version,
		}); err != nil {
		return Endpoint{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Endpoint{}, fmt.Errorf("commit webhook update: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) RotateSecret(
	ctx context.Context,
	params RotateSecretParams,
) (Endpoint, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Endpoint{}, fmt.Errorf("begin webhook secret rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result, err := scanEndpoint(tx.QueryRow(ctx, `
		UPDATE webhook_endpoints
		SET secret_version = $3, secret_ciphertext = $4,
		    version = version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND version = $5
		RETURNING id::text, workspace_id::text, display_name, endpoint_url,
		          event_types, state, version, secret_ciphertext IS NOT NULL,
		          created_at, updated_at`,
		params.EndpointID, params.WorkspaceID, params.SecretVersion,
		params.SecretCiphertext, params.ExpectedVersion,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrConflict
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("rotate webhook secret: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.UpdatedBy,
		"webhook.secret_rotated", "webhook", params.EndpointID, map[string]any{
			"version": result.Version, "secret_version": params.SecretVersion,
		}); err != nil {
		return Endpoint{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Endpoint{}, fmt.Errorf("commit webhook secret rotation: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) EnqueueTest(
	ctx context.Context,
	params EnqueueTestParams,
) (Delivery, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, fmt.Errorf("begin webhook test enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result, err := scanDelivery(tx.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (
			id, workspace_id, webhook_id, webhook_version, event_id, event_type,
			payload, state, attempts, max_attempts, available_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'webhook.test', $6, 'pending', 0, 5, $7, $7, $7)
		RETURNING id::text, workspace_id::text, webhook_id::text, webhook_version,
		          notification_id::text, event_id::text, event_type, payload,
		          state, attempts, max_attempts, available_at, response_status,
		          last_error_code, delivered_at, created_at, updated_at`,
		params.DeliveryID, params.WorkspaceID, params.WebhookID, params.WebhookVersion,
		params.EventID, params.Payload, params.CreatedAt,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return Delivery{}, ErrConflict
		}
		return Delivery{}, fmt.Errorf("insert webhook test delivery: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.CreatedBy,
		"webhook.test_enqueued", "webhook_delivery", params.DeliveryID, map[string]any{
			"webhook_id": params.WebhookID, "webhook_version": params.WebhookVersion,
		}); err != nil {
		return Delivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Delivery{}, fmt.Errorf("commit webhook test enqueue: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) Claim(
	ctx context.Context,
	params ClaimParams,
) (DeliveryAttempt, error) {
	leaseDuration := params.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = defaultDeliveryLease
	}
	var result DeliveryAttempt
	err := repository.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT delivery.sequence
			FROM webhook_deliveries AS delivery
			JOIN webhook_endpoints AS endpoint
			  ON endpoint.id = delivery.webhook_id
			 AND endpoint.workspace_id = delivery.workspace_id
			WHERE endpoint.state = 'enabled'
			  AND delivery.webhook_version = endpoint.version
			  AND delivery.attempts < delivery.max_attempts
			  AND delivery.available_at <= $2
			  AND (
				 delivery.state IN ('pending', 'retry_wait')
				 OR (delivery.state = 'delivering' AND delivery.lease_expires_at <= $2)
			  )
			ORDER BY delivery.sequence
			FOR UPDATE OF delivery SKIP LOCKED
			LIMIT 1
		)
		UPDATE webhook_deliveries AS delivery
		SET state = 'delivering', attempts = delivery.attempts + 1,
		    lease_owner = $1, lease_expires_at = $2 + $3::interval,
		    updated_at = $2
		FROM candidate, webhook_endpoints AS endpoint
		WHERE delivery.sequence = candidate.sequence
		  AND endpoint.id = delivery.webhook_id
		  AND endpoint.workspace_id = delivery.workspace_id
		RETURNING delivery.id::text, delivery.workspace_id::text,
		          delivery.webhook_id::text, delivery.webhook_version,
		          delivery.notification_id::text, delivery.event_id::text,
		          delivery.event_type, delivery.payload, delivery.state,
		          delivery.attempts, delivery.max_attempts,
		          delivery.available_at, delivery.response_status,
		          delivery.last_error_code, delivery.delivered_at,
		          delivery.created_at, delivery.updated_at,
		          endpoint.endpoint_url, endpoint.secret_version,
		          endpoint.secret_ciphertext, delivery.lease_owner`,
		params.WorkerID, params.Now.UTC(), formatInterval(leaseDuration),
	).Scan(
		&result.ID, &result.WorkspaceID, &result.WebhookID, &result.WebhookVersion,
		&result.NotificationID, &result.EventID, &result.EventType, &result.Payload,
		&result.State, &result.Attempts, &result.MaxAttempts, &result.AvailableAt,
		&result.ResponseStatus, &result.LastErrorCode, &result.DeliveredAt,
		&result.CreatedAt, &result.UpdatedAt, &result.EndpointURL,
		&result.SecretVersion, &result.SecretCiphertext, &result.LeaseOwner,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeliveryAttempt{}, ErrNoDelivery
	}
	if err != nil {
		return DeliveryAttempt{}, fmt.Errorf("claim webhook delivery: %w", err)
	}
	result.Payload = append([]byte(nil), result.Payload...)
	result.SecretCiphertext = append([]byte(nil), result.SecretCiphertext...)
	return result, nil
}

func (repository *PostgresRepository) Succeed(
	ctx context.Context,
	params CompleteParams,
) error {
	command, err := repository.pool.Exec(ctx, `
		UPDATE webhook_deliveries
		SET state = 'succeeded', response_status = $3, delivered_at = $4,
		    lease_owner = NULL, lease_expires_at = NULL, updated_at = $4
		WHERE id = $1 AND state = 'delivering' AND lease_owner = $2`,
		params.DeliveryID, params.WorkerID, params.ResponseStatus, params.Now.UTC())
	if err != nil {
		return fmt.Errorf("mark webhook delivery succeeded: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (repository *PostgresRepository) Fail(
	ctx context.Context,
	params FailParams,
) error {
	command, err := repository.pool.Exec(ctx, `
		UPDATE webhook_deliveries
		SET state = CASE
		             WHEN NOT $6 OR attempts >= max_attempts THEN 'failed'
		             ELSE 'retry_wait'
		           END,
		    available_at = CASE
		                    WHEN NOT $6 OR attempts >= max_attempts THEN available_at
		                    ELSE $4
		                  END,
		    response_status = $5, last_error_code = $3,
		    lease_owner = NULL, lease_expires_at = NULL, updated_at = $2
		WHERE id = $1 AND state = 'delivering' AND lease_owner = $7`,
		params.DeliveryID, params.Now.UTC(), params.ErrorCode, params.RetryAt.UTC(),
		params.ResponseStatus, params.Retryable, params.WorkerID,
	)
	if err != nil {
		return fmt.Errorf("mark webhook delivery failed: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func formatInterval(duration time.Duration) string {
	return fmt.Sprintf("%f seconds", duration.Seconds())
}

func scanEndpoint(row interface{ Scan(...any) error }) (Endpoint, error) {
	var result Endpoint
	if err := row.Scan(
		&result.ID, &result.WorkspaceID, &result.DisplayName, &result.URL,
		&result.EventTypes, &result.State, &result.Version, &result.SecretConfigured,
		&result.CreatedAt, &result.UpdatedAt,
	); err != nil {
		return Endpoint{}, fmt.Errorf("scan webhook endpoint: %w", err)
	}
	return result, nil
}

func scanDelivery(row interface{ Scan(...any) error }) (Delivery, error) {
	var result Delivery
	if err := row.Scan(
		&result.ID, &result.WorkspaceID, &result.WebhookID, &result.WebhookVersion,
		&result.NotificationID, &result.EventID, &result.EventType, &result.Payload,
		&result.State, &result.Attempts, &result.MaxAttempts, &result.AvailableAt,
		&result.ResponseStatus, &result.LastErrorCode, &result.DeliveredAt,
		&result.CreatedAt, &result.UpdatedAt,
	); err != nil {
		return Delivery{}, fmt.Errorf("scan webhook delivery: %w", err)
	}
	result.Payload = append([]byte(nil), result.Payload...)
	return result, nil
}

func insertAudit(
	ctx context.Context,
	tx interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	auditID, workspaceID, actorID, action, targetType, targetID string,
	metadata any,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', $4, $5, $6, $7)`,
		auditID, workspaceID, actorID, action, targetType, targetID, metadata,
	); err != nil {
		return fmt.Errorf("insert webhook audit: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

var _ Repository = (*PostgresRepository)(nil)
