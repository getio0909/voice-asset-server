package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	integrationWorkspace      = "12000000-0000-4000-8000-000000000001"
	integrationOtherWorkspace = "12000000-0000-4000-8000-000000000002"
	integrationOwner          = "22000000-0000-4000-8000-000000000001"
	integrationOtherOwner     = "22000000-0000-4000-8000-000000000002"
)

func TestPostgresWorkspaceProfileIsScopedVersionedAndAudited(t *testing.T) {
	pool := migratedWorkspacePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	seedWorkspaceFixtures(t, ctx, pool)

	service := NewService(NewPostgresRepository(pool))
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x53}, 64))
	service.now = func() time.Time { return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) }
	principal := auth.Principal{
		UserID: integrationOwner, WorkspaceID: integrationWorkspace, Role: "owner", Scopes: auth.AllScopes(),
	}

	before, err := service.Get(ctx, principal)
	if err != nil || before.ID != integrationWorkspace || before.Name != "Primary" || before.Version != 1 {
		t.Fatalf("Get() = (%+v, %v)", before, err)
	}
	updated, err := service.Update(ctx, principal, before.Version, UpdateInput{Name: "Renamed"}, "workspace-update")
	if err != nil || updated.ID != integrationWorkspace || updated.Name != "Renamed" || updated.Version != 2 {
		t.Fatalf("Update() = (%+v, %v)", updated, err)
	}
	if !updated.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated timestamp = %s, before = %s", updated.UpdatedAt, before.UpdatedAt)
	}
	if _, err := service.Update(ctx, principal, 1, UpdateInput{Name: "Stale"}, "workspace-stale"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}
	unchanged, err := service.Update(ctx, principal, 2, UpdateInput{Name: "Renamed"}, "workspace-noop")
	if err != nil || unchanged.Version != 2 || !unchanged.UpdatedAt.Equal(updated.UpdatedAt) {
		t.Fatalf("unchanged Update() = (%+v, %v)", unchanged, err)
	}

	other, err := service.Get(ctx, auth.Principal{
		UserID: integrationOtherOwner, WorkspaceID: integrationOtherWorkspace,
		Role: "owner", Scopes: []string{auth.ScopeAdminRead},
	})
	if err != nil || other.Name != "Other" || other.Version != 1 {
		t.Fatalf("other Get() = (%+v, %v)", other, err)
	}

	var targetID, requestID string
	var metadata []byte
	if err := pool.QueryRow(ctx, `
		SELECT target_id::text, request_id, metadata
		FROM audit_logs WHERE workspace_id = $1 AND action = 'workspace.updated'`, integrationWorkspace,
	).Scan(&targetID, &requestID, &metadata); err != nil {
		t.Fatalf("query workspace audit: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(metadata, &fields); err != nil {
		t.Fatalf("decode workspace audit: %v", err)
	}
	if targetID != integrationWorkspace || requestID != "workspace-update" ||
		fields["previous_name"] != "Primary" || fields["name"] != "Renamed" ||
		fields["previous_version"] != float64(1) || fields["version"] != float64(2) {
		t.Fatalf("workspace audit = target:%q request:%q metadata:%v", targetID, requestID, fields)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'workspace.updated'`, integrationWorkspace).Scan(&auditCount); err != nil {
		t.Fatalf("count workspace audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("workspace update audits = %d, want 1", auditCount)
	}
}

func seedWorkspaceFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other');
		INSERT INTO users (id, email, password_hash, status) VALUES
			($3, 'workspace.owner@example.test', 'hash', 'active'),
			($4, 'workspace.other@example.test', 'hash', 'active');
		INSERT INTO memberships (workspace_id, user_id, role) VALUES
			($1, $3, 'owner'), ($2, $4, 'owner');`,
		integrationWorkspace, integrationOtherWorkspace, integrationOwner, integrationOtherOwner)
	if err != nil {
		t.Fatalf("seed workspace fixtures: %v", err)
	}
}

func migratedWorkspacePool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("workspace_test_%d", time.Now().UnixNano())
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
