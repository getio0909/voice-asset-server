package notification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	integrationWorkspaceID = "11000000-0000-4000-8000-0000000000a1"
	integrationUserID      = "21000000-0000-4000-8000-0000000000a1"
	integrationOtherUserID = "21000000-0000-4000-8000-0000000000a2"
	integrationAssetID     = "31000000-0000-4000-8000-0000000000a1"
	integrationBackfillJob = "41000000-0000-4000-8000-0000000000a1"
	integrationOtherJob    = "41000000-0000-4000-8000-0000000000a2"
	integrationRetryJob    = "41000000-0000-4000-8000-0000000000a3"
	integrationRollbackJob = "41000000-0000-4000-8000-0000000000a4"
)

func TestPostgresNotificationFeedBackfillsAndCapturesTerminalTransitions(t *testing.T) {
	pool, files := notificationPoolBeforeLatest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO workspaces (id, name) VALUES ($1, 'Notifications')`, []any{integrationWorkspaceID}},
		{`INSERT INTO users (id, email, password_hash, status) VALUES
			($1, 'events@example.test', 'hash', 'active'),
			($2, 'other-events@example.test', 'hash', 'active')`, []any{integrationUserID, integrationOtherUserID}},
		{`INSERT INTO memberships (workspace_id, user_id, role) VALUES
			($1, $2, 'editor'), ($1, $3, 'viewer')`, []any{integrationWorkspaceID, integrationUserID, integrationOtherUserID}},
		{`INSERT INTO assets (id, workspace_id, title, language, status, created_by)
			VALUES ($1, $2, 'Notification source', 'en', 'ready', $3)`, []any{integrationAssetID, integrationWorkspaceID, integrationUserID}},
		{`INSERT INTO jobs (id, workspace_id, asset_id, kind, state, created_by, payload)
			VALUES
			($1, $3, $4, 'mock_transcribe', 'succeeded', $5, jsonb_build_object('secret_like', 'must-not-escape')),
			($2, $3, $4, 'mock_transcribe', 'failed', $6, jsonb_build_object('secret_like', 'must-not-escape'))`,
			[]any{integrationBackfillJob, integrationOtherJob, integrationWorkspaceID, integrationAssetID, integrationUserID, integrationOtherUserID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed pre-notification data: %v", err)
		}
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire notification migration connection")
	}
	applied, err := migration.Apply(ctx, connection.Conn(), files)
	connection.Release()
	if err != nil || applied != 1 {
		t.Fatalf("apply notification migration = (%d, %v), want (1, nil)", applied, err)
	}

	repository := NewPostgresRepository(pool)
	page, err := repository.List(ctx, ListParams{
		WorkspaceID: integrationWorkspaceID, RecipientUserID: integrationUserID, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("List(backfill) = (%+v, %v)", page, err)
	}
	backfill := page.Items[0]
	if backfill.ID == "" || backfill.Type != TypeJobSucceeded || backfill.JobID != integrationBackfillJob ||
		backfill.JobKind != "mock_transcribe" || backfill.State != StateSucceeded ||
		backfill.AssetID == nil || *backfill.AssetID != integrationAssetID || backfill.ErrorCode != nil ||
		backfill.Sequence != page.HighWatermark {
		t.Fatalf("backfilled event/page = %+v/%+v", backfill, page)
	}
	if _, err := pool.Exec(ctx, `UPDATE notifications SET job_kind = 'tampered' WHERE id = $1`, backfill.ID); err == nil {
		t.Fatal("immutable notification accepted an update")
	}

	other, err := repository.List(ctx, ListParams{
		WorkspaceID: integrationWorkspaceID, RecipientUserID: integrationOtherUserID, Limit: 10,
	})
	if err != nil || len(other.Items) != 1 || other.Items[0].JobID != integrationOtherJob {
		t.Fatalf("List(other user) = (%+v, %v)", other, err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO jobs (id, workspace_id, asset_id, kind, state, created_by, payload)
		VALUES ($1, $2, $3, 'mock_transcribe', 'queued', $4, '{}'),
		       ($5, $2, NULL, 'generate_waveform', 'queued', $4, '{}')`,
		integrationRetryJob, integrationWorkspaceID, integrationAssetID,
		integrationUserID, integrationRollbackJob); err != nil {
		t.Fatalf("seed transition jobs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE jobs SET state = 'failed', last_error_code = 'provider_unavailable',
		updated_at = clock_timestamp() WHERE id = $1`, integrationRetryJob); err != nil {
		t.Fatalf("commit failed transition: %v", err)
	}

	var beforeRollback int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE recipient_user_id = $1`, integrationUserID).Scan(&beforeRollback); err != nil {
		t.Fatalf("count notifications before rollback: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin notification rollback: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET state = 'cancelled', updated_at = clock_timestamp() WHERE id = $1`, integrationRollbackJob); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("update rollback transition: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback terminal transition: %v", err)
	}
	var afterRollback int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE recipient_user_id = $1`, integrationUserID).Scan(&afterRollback); err != nil {
		t.Fatalf("count notifications after rollback: %v", err)
	}
	if afterRollback != beforeRollback {
		t.Fatalf("rolled-back transition retained %d notifications, want %d", afterRollback, beforeRollback)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE jobs SET state = 'queued', last_error_code = NULL, updated_at = clock_timestamp() WHERE id = $1;
		UPDATE jobs SET state = 'succeeded', updated_at = clock_timestamp() WHERE id = $1`, integrationRetryJob); err != nil {
		t.Fatalf("commit retried success: %v", err)
	}
	page, err = repository.List(ctx, ListParams{
		WorkspaceID: integrationWorkspaceID, RecipientUserID: integrationUserID, Limit: 10,
	})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("List(transitions) = (%+v, %v)", page, err)
	}
	failed, succeeded := page.Items[1], page.Items[2]
	if failed.Type != TypeJobFailed || failed.State != StateFailed || failed.ErrorCode == nil ||
		*failed.ErrorCode != "provider_unavailable" || failed.JobID != integrationRetryJob {
		t.Fatalf("failed event = %+v", failed)
	}
	if succeeded.Type != TypeJobSucceeded || succeeded.State != StateSucceeded || succeeded.ErrorCode != nil ||
		succeeded.JobID != integrationRetryJob || !(backfill.Sequence < failed.Sequence && failed.Sequence < succeeded.Sequence) ||
		page.HighWatermark != succeeded.Sequence {
		t.Fatalf("succeeded event/page = %+v/%+v", succeeded, page)
	}
}

func notificationPoolBeforeLatest(t *testing.T) (*pgxpool.Pool, []migration.File) {
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
	schema := fmt.Sprintf("notification_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create notification schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop notification schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("create notification pool")
	}
	t.Cleanup(pool.Close)
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	notificationMigrationIndex := -1
	for index, file := range files {
		if file.Version == 17 {
			notificationMigrationIndex = index
			break
		}
	}
	if notificationMigrationIndex < 1 {
		t.Fatalf("notification migration is missing: %+v", files)
	}
	preNotificationFiles := files[:notificationMigrationIndex]
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire pre-notification migration connection")
	}
	if applied, err := migration.Apply(ctx, connection.Conn(), preNotificationFiles); err != nil || applied != len(preNotificationFiles) {
		connection.Release()
		t.Fatalf("apply pre-notification migrations = (%d, %v)", applied, err)
	}
	connection.Release()
	return pool, files[:notificationMigrationIndex+1]
}
