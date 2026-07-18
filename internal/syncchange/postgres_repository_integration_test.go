package syncchange

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
	integrationWorkspaceID      = "11000000-0000-4000-8000-000000000091"
	integrationOtherWorkspaceID = "11000000-0000-4000-8000-000000000092"
	integrationUserID           = "21000000-0000-4000-8000-000000000091"
	integrationAssetID          = "31000000-0000-4000-8000-000000000091"
	integrationOtherAssetID     = "31000000-0000-4000-8000-000000000092"
)

func TestPostgresChangeFeedBackfillsAndCapturesCommittedMutations(t *testing.T) {
	pool, files := syncChangePoolBeforeLatest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO workspaces (id, name) VALUES ($1, 'Sync'), ($2, 'Other')`, []any{integrationWorkspaceID, integrationOtherWorkspaceID}},
		{`INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'sync@example.test', 'hash', 'active')`, []any{integrationUserID}},
		{`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`, []any{integrationWorkspaceID, integrationOtherWorkspaceID, integrationUserID}},
		{`INSERT INTO assets (id, workspace_id, title, language, status, duration_ms, created_by) VALUES
			($1, $3, 'Backfilled recording', 'en-US', 'ready', 1200, $5),
			($2, $4, 'Other recording', 'en', 'draft', NULL, $5)`, []any{integrationAssetID, integrationOtherAssetID, integrationWorkspaceID, integrationOtherWorkspaceID, integrationUserID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed pre-sync data: %v", err)
		}
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire latest-migration connection")
	}
	applied, err := migration.Apply(ctx, connection.Conn(), files)
	connection.Release()
	if err != nil || applied != 1 {
		t.Fatalf("apply sync migration = (%d, %v), want (1, nil)", applied, err)
	}

	repository := NewPostgresRepository(pool)
	page, err := repository.List(ctx, ListParams{WorkspaceID: integrationWorkspaceID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("List(backfill) = (%+v, %v)", page, err)
	}
	backfill := page.Items[0]
	if backfill.Sequence < 1 || backfill.Operation != "upsert" || backfill.EntityID != integrationAssetID ||
		backfill.EntityVersion != 1 || backfill.Asset == nil || backfill.Asset.Title != "Backfilled recording" ||
		backfill.Asset.Language != "en-US" || backfill.Asset.DurationMS == nil || *backfill.Asset.DurationMS != 1200 {
		t.Fatalf("backfilled change = %+v", backfill)
	}
	otherPage, err := repository.List(ctx, ListParams{WorkspaceID: integrationOtherWorkspaceID, Limit: 10})
	if err != nil || len(otherPage.Items) != 1 || otherPage.Items[0].EntityID != integrationOtherAssetID {
		t.Fatalf("List(other workspace) = (%+v, %v)", otherPage, err)
	}

	var beforeRollback int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sync_changes WHERE workspace_id = $1`, integrationWorkspaceID).Scan(&beforeRollback); err != nil {
		t.Fatalf("count pre-rollback changes: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback mutation: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE assets SET title = 'Rolled back', version = version + 1 WHERE id = $1`, integrationAssetID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("update rollback mutation: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback mutation: %v", err)
	}
	var afterRollback int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sync_changes WHERE workspace_id = $1`, integrationWorkspaceID).Scan(&afterRollback); err != nil {
		t.Fatalf("count post-rollback changes: %v", err)
	}
	if afterRollback != beforeRollback {
		t.Fatalf("rollback retained %d changes, want %d", afterRollback, beforeRollback)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE assets
		SET title = 'Updated recording', status = 'trashed', status_before_trash = 'ready',
		    deleted_at = clock_timestamp(), version = version + 1, updated_at = clock_timestamp()
		WHERE id = $1`, integrationAssetID); err != nil {
		t.Fatalf("commit asset update: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM assets WHERE id = $1`, integrationAssetID); err != nil {
		t.Fatalf("commit asset delete: %v", err)
	}

	page, err = repository.List(ctx, ListParams{WorkspaceID: integrationWorkspaceID, Limit: 10})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("List(mutations) = (%+v, %v)", page, err)
	}
	update := page.Items[1]
	deletion := page.Items[2]
	if update.Operation != "upsert" || update.Asset == nil || update.Asset.Title != "Updated recording" ||
		update.Asset.Status != "trashed" || update.Asset.TrashedAt == nil || update.EntityVersion != 2 {
		t.Fatalf("update change = %+v", update)
	}
	if deletion.Operation != "delete" || deletion.Asset != nil || deletion.EntityID != integrationAssetID ||
		deletion.EntityVersion != 2 || page.HighWatermark != deletion.Sequence {
		t.Fatalf("deletion change/page = %+v/%+v", deletion, page)
	}
	if !(page.Items[0].Sequence < update.Sequence && update.Sequence < deletion.Sequence) {
		t.Fatalf("change order = %d, %d, %d", page.Items[0].Sequence, update.Sequence, deletion.Sequence)
	}
}

func syncChangePoolBeforeLatest(t *testing.T) (*pgxpool.Pool, []migration.File) {
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
	schema := fmt.Sprintf("sync_change_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
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
		t.Fatal("create pool")
	}
	t.Cleanup(pool.Close)
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	syncMigrationIndex := -1
	for index, file := range files {
		if file.Version == 15 {
			syncMigrationIndex = index
			break
		}
	}
	if syncMigrationIndex < 1 {
		t.Fatalf("unexpected latest migration set: %+v", files)
	}
	preSyncFiles := files[:syncMigrationIndex]
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire migration connection")
	}
	if applied, err := migration.Apply(ctx, connection.Conn(), preSyncFiles); err != nil || applied != len(preSyncFiles) {
		connection.Release()
		t.Fatalf("apply pre-sync migrations = (%d, %v)", applied, err)
	}
	connection.Release()
	return pool, files[:syncMigrationIndex+1]
}
