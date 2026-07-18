package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryWebhookLifecycleAndNotificationProjection(t *testing.T) {
	pool := migratedWebhookPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "10000000-0000-4000-8000-000000000081"
		userID      = "20000000-0000-4000-8000-000000000081"
		webhookID   = "30000000-0000-4000-8000-000000000081"
		deliveryID  = "40000000-0000-4000-8000-000000000081"
		eventID     = "50000000-0000-4000-8000-000000000081"
		jobID       = "60000000-0000-4000-8000-000000000081"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Webhook test')", workspaceID); err != nil {
		t.Fatal("seed workspace")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'webhook-owner@example.com', 'encoded', 'active')", userID); err != nil {
		t.Fatal("seed user")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')", workspaceID, userID); err != nil {
		t.Fatal("seed membership")
	}

	repository := NewPostgresRepository(pool)
	secretCiphertext := []byte("encrypted-webhook-secret")
	created, err := repository.Create(ctx, CreateParams{
		EndpointID: webhookID, AuditID: "70000000-0000-4000-8000-000000000081",
		WorkspaceID: workspaceID, CreatedBy: userID, RequestID: "req-create",
		DisplayName: "Build events", URL: "https://example.com/hooks/build",
		EventTypes: []string{EventJobSucceeded, EventJobFailed}, State: StateEnabled,
		Version: 1, SecretCiphertext: secretCiphertext,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Version != 1 || !created.SecretConfigured {
		t.Fatalf("created endpoint = %+v", created)
	}
	stored, err := repository.GetStored(ctx, workspaceID, webhookID)
	if err != nil || stored.SecretVersion != 1 || !bytes.Equal(stored.SecretCiphertext, secretCiphertext) {
		t.Fatalf("GetStored() = %+v, error = %v", stored, err)
	}
	listed, err := repository.List(ctx, workspaceID)
	if err != nil || len(listed) != 1 || listed[0].ID != webhookID {
		t.Fatalf("List() = %+v, error = %v", listed, err)
	}

	updated, err := repository.Update(ctx, UpdateParams{
		EndpointID: webhookID, AuditID: "70000000-0000-4000-8000-000000000082",
		WorkspaceID: workspaceID, UpdatedBy: userID, RequestID: "req-update",
		DisplayName: "Build and failure events", URL: stored.URL,
		EventTypes: []string{EventJobSucceeded, EventJobFailed}, State: StateEnabled,
		ExpectedVersion: 1,
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("Update() = %+v, error = %v", updated, err)
	}
	if _, err := repository.Update(ctx, UpdateParams{
		EndpointID: webhookID, AuditID: "70000000-0000-4000-8000-000000000083",
		WorkspaceID: workspaceID, UpdatedBy: userID, RequestID: "req-stale",
		DisplayName: "stale", URL: stored.URL,
		EventTypes: []string{EventJobSucceeded}, State: StateEnabled,
		ExpectedVersion: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}

	createdAt := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	testDelivery, err := repository.EnqueueTest(ctx, EnqueueTestParams{
		DeliveryID: deliveryID, EventID: eventID,
		AuditID: "70000000-0000-4000-8000-000000000084", WorkspaceID: workspaceID,
		WebhookID: webhookID, WebhookVersion: 2, RequestID: "req-test",
		CreatedBy: userID, CreatedAt: createdAt,
		Payload: []byte(`{"id":"50000000-0000-4000-8000-000000000081","type":"webhook.test"}`),
	})
	if err != nil || testDelivery.State != DeliveryPending || testDelivery.Attempts != 0 {
		t.Fatalf("EnqueueTest() = %+v, error = %v", testDelivery, err)
	}

	claimed, err := repository.Claim(ctx, ClaimParams{
		WorkerID: "worker-integration", Now: createdAt.Add(time.Minute), LeaseDuration: time.Minute,
	})
	if err != nil || claimed.ID != deliveryID || claimed.Attempts != 1 || claimed.LeaseOwner != "worker-integration" {
		t.Fatalf("Claim(test) = %+v, error = %v", claimed, err)
	}
	if err := repository.Succeed(ctx, CompleteParams{
		DeliveryID: deliveryID, WorkerID: "worker-integration", Now: createdAt.Add(2 * time.Minute), ResponseStatus: 204,
	}); err != nil {
		t.Fatalf("Succeed() error = %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO jobs (id, workspace_id, created_by, kind, state, updated_at)
		VALUES ($1, $2, $3, 'transcribe', 'succeeded', $4)`,
		jobID, workspaceID, userID, createdAt.Add(3*time.Minute)); err != nil {
		t.Fatal("insert terminal job")
	}
	var projectedState string
	var projectedVersion int64
	if err := pool.QueryRow(ctx, `
		SELECT state, webhook_version FROM webhook_deliveries
		WHERE webhook_id = $1 AND event_type = 'job.succeeded'`, webhookID).
		Scan(&projectedState, &projectedVersion); err != nil {
		t.Fatal("read notification projection")
	}
	if projectedState != DeliveryPending || projectedVersion != 2 {
		t.Fatalf("notification projection state=%q version=%d", projectedState, projectedVersion)
	}

	claimed, err = repository.Claim(ctx, ClaimParams{
		WorkerID: "worker-integration", Now: createdAt.Add(4 * time.Minute), LeaseDuration: time.Minute,
	})
	if err != nil || claimed.EventType != EventJobSucceeded {
		t.Fatalf("Claim(notification) = %+v, error = %v", claimed, err)
	}
	responseStatus := 503
	if err := repository.Fail(ctx, FailParams{
		DeliveryID: claimed.ID, WorkerID: claimed.LeaseOwner,
		Now: createdAt.Add(5 * time.Minute), RetryAt: createdAt.Add(10 * time.Minute),
		ResponseStatus: &responseStatus, ErrorCode: DeliveryErrorHTTPServer, Retryable: true,
	}); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if _, err := repository.Update(ctx, UpdateParams{
		EndpointID: webhookID, AuditID: "70000000-0000-4000-8000-000000000085",
		WorkspaceID: workspaceID, UpdatedBy: userID, RequestID: "req-disable",
		DisplayName: updated.DisplayName, URL: updated.URL,
		EventTypes: updated.EventTypes, State: StateDisabled, ExpectedVersion: 2,
	}); err != nil {
		t.Fatalf("disable endpoint error = %v", err)
	}
	var cancelled int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM webhook_deliveries
		WHERE webhook_id = $1 AND event_type = 'job.succeeded' AND state = 'cancelled'`, webhookID).Scan(&cancelled); err != nil || cancelled != 1 {
		t.Fatalf("cancelled deliveries = %d, error = %v", cancelled, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE workspace_id = $1 AND action LIKE 'webhook.%'`, workspaceID).Scan(&auditCount); err != nil || auditCount != 4 {
		t.Fatalf("webhook audit count = %d, error = %v", auditCount, err)
	}
}

func migratedWebhookPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect to PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("webhook_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal("create isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Error("drop isolated schema")
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("create pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire migration connection")
	}
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		connection.Release()
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := migration.Apply(ctx, connection.Conn(), files); err != nil {
		connection.Release()
		t.Fatalf("apply migrations: %v", err)
	}
	connection.Release()
	return pool
}
