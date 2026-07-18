package hotword

import (
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

func TestPostgresRepositoryVersionsScopesAndAuditsHotwordSets(t *testing.T) {
	pool := migratedHotwordPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID       = "10000000-0000-4000-8000-000000000092"
		otherWorkspaceID  = "10000000-0000-4000-8000-000000000093"
		userID            = "20000000-0000-4000-8000-000000000092"
		assetID           = "30000000-0000-4000-8000-000000000092"
		workspaceSetID    = "40000000-0000-4000-8000-000000000092"
		workspaceVersion1 = "50000000-0000-4000-8000-000000000092"
		workspaceVersion2 = "50000000-0000-4000-8000-000000000093"
		assetSetID        = "40000000-0000-4000-8000-000000000093"
		assetVersionID    = "50000000-0000-4000-8000-000000000094"
	)
	seed := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", []any{workspaceID, otherWorkspaceID}},
		{"INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'owner@example.com', 'encoded', 'active')", []any{userID}},
		{"INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $3, 'owner'), ($2, $3, 'owner')", []any{workspaceID, otherWorkspaceID, userID}},
		{"INSERT INTO assets (id, workspace_id, title, language, status, created_by) VALUES ($1, $2, 'Recording', 'zh-CN', 'ready', $3)", []any{assetID, workspaceID, userID}},
	}
	for _, statement := range seed {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal("seed hotword fixture")
		}
	}
	workspaceEntries, err := normalizeAndEncodeEntries([]EntryInput{{
		Term: "voiceasset", Aliases: []string{"GetIO"}, Language: "zh-CN", Weight: 30,
	}})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresRepository(pool)
	workspaceSet, err := repository.Create(ctx, CreateParams{
		SetID: workspaceSetID, VersionID: workspaceVersion1,
		AuditID:     "60000000-0000-4000-8000-000000000092",
		WorkspaceID: workspaceID, CreatedBy: userID, DisplayName: "Workspace terms",
		ScopeType: ScopeWorkspace, State: StateEnabled, EntriesJSON: workspaceEntries,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if workspaceSet.CurrentVersion != 1 || workspaceSet.ResourceVersion != 1 ||
		len(workspaceSet.Entries) != 1 || workspaceSet.Entries[0].Term != "voiceasset" {
		t.Fatalf("Create() = %+v", workspaceSet)
	}
	if _, err := repository.Create(ctx, CreateParams{
		SetID:       "40000000-0000-4000-8000-000000000094",
		VersionID:   "50000000-0000-4000-8000-000000000095",
		AuditID:     "60000000-0000-4000-8000-000000000093",
		WorkspaceID: workspaceID, CreatedBy: userID, DisplayName: "workspace TERMS",
		ScopeType: ScopeWorkspace, State: StateEnabled, EntriesJSON: workspaceEntries,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Create() error = %v, want ErrConflict", err)
	}
	versionTwoEntries, _ := normalizeAndEncodeEntries([]EntryInput{{
		Term: "VoiceAsset", Language: "zh-CN", Weight: 40,
	}})
	workspaceSet, err = repository.AddVersion(ctx, AddVersionParams{
		SetID: workspaceSetID, VersionID: workspaceVersion2,
		AuditID:     "60000000-0000-4000-8000-000000000094",
		WorkspaceID: workspaceID, CreatedBy: userID,
		ExpectedResourceVersion: 1, EntriesJSON: versionTwoEntries,
	})
	if err != nil || workspaceSet.CurrentVersion != 2 || workspaceSet.ResourceVersion != 2 ||
		workspaceSet.Entries[0].Weight != 40 {
		t.Fatalf("AddVersion() = (%+v, %v)", workspaceSet, err)
	}
	if _, err := repository.AddVersion(ctx, AddVersionParams{
		SetID: workspaceSetID, VersionID: "50000000-0000-4000-8000-000000000096",
		AuditID:     "60000000-0000-4000-8000-000000000095",
		WorkspaceID: workspaceID, CreatedBy: userID,
		ExpectedResourceVersion: 1, EntriesJSON: versionTwoEntries,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale AddVersion() error = %v, want ErrConflict", err)
	}
	workspaceSet, err = repository.UpdateState(ctx, UpdateStateParams{
		SetID: workspaceSetID, AuditID: "60000000-0000-4000-8000-000000000096",
		WorkspaceID: workspaceID, UpdatedBy: userID,
		State: StateDisabled, ExpectedResourceVersion: 2,
	})
	if err != nil || workspaceSet.State != StateDisabled || workspaceSet.ResourceVersion != 3 ||
		workspaceSet.CurrentVersion != 2 {
		t.Fatalf("UpdateState() = (%+v, %v)", workspaceSet, err)
	}
	assetEntries, _ := normalizeAndEncodeEntries([]EntryInput{{
		Term: "VoiceAsset", Language: "zh-CN", Weight: 95,
	}})
	assetScope := assetID
	assetSet, err := repository.Create(ctx, CreateParams{
		SetID: assetSetID, VersionID: assetVersionID,
		AuditID:     "60000000-0000-4000-8000-000000000097",
		WorkspaceID: workspaceID, CreatedBy: userID, DisplayName: "Asset terms",
		ScopeType: ScopeAsset, ScopeID: &assetScope,
		State: StateEnabled, EntriesJSON: assetEntries,
	})
	if err != nil || assetSet.ScopeID == nil || *assetSet.ScopeID != assetID {
		t.Fatalf("asset Create() = (%+v, %v)", assetSet, err)
	}
	missingAsset := "30000000-0000-4000-8000-000000000099"
	if _, err := repository.Create(ctx, CreateParams{
		SetID:       "40000000-0000-4000-8000-000000000095",
		VersionID:   "50000000-0000-4000-8000-000000000097",
		AuditID:     "60000000-0000-4000-8000-000000000098",
		WorkspaceID: workspaceID, CreatedBy: userID, DisplayName: "Missing asset",
		ScopeType: ScopeAsset, ScopeID: &missingAsset,
		State: StateEnabled, EntriesJSON: assetEntries,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing asset Create() error = %v, want ErrNotFound", err)
	}
	listed, err := repository.List(ctx, workspaceID)
	if err != nil || len(listed) != 2 || listed[0].ID != workspaceSetID || listed[1].ID != assetSetID {
		t.Fatalf("List() = (%+v, %v)", listed, err)
	}
	effective, err := repository.Resolve(ctx, workspaceID, assetID)
	if err != nil || len(effective) != 1 || effective[0].ID != assetSetID ||
		effective[0].Entries[0].Weight != 95 {
		t.Fatalf("Resolve() = (%+v, %v)", effective, err)
	}
	if other, err := repository.List(ctx, otherWorkspaceID); err != nil || len(other) != 0 {
		t.Fatalf("cross-workspace List() = (%+v, %v)", other, err)
	}
	if _, err := repository.UpdateState(ctx, UpdateStateParams{
		SetID: assetSetID, AuditID: "60000000-0000-4000-8000-000000000099",
		WorkspaceID: otherWorkspaceID, UpdatedBy: userID,
		State: StateDisabled, ExpectedResourceVersion: 1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace UpdateState() error = %v, want ErrNotFound", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE workspace_id = $1 AND target_type = 'hotword_set'`, workspaceID,
	).Scan(&auditCount); err != nil || auditCount != 4 {
		t.Fatalf("audit count = %d, error = %v", auditCount, err)
	}
}

func migratedHotwordPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("hotword_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal("create isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
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
