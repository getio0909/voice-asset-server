package providerprofile

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

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryPersistsEncryptedWorkspaceProfilesAndAudit(t *testing.T) {
	pool := migratedProviderProfilePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "10000000-0000-4000-8000-000000000071"
		otherSpace  = "10000000-0000-4000-8000-000000000072"
		userID      = "20000000-0000-4000-8000-000000000071"
		mockID      = "90000000-0000-4000-8000-000000000071"
		tencentID   = "90000000-0000-4000-8000-000000000072"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", workspaceID, otherSpace); err != nil {
		t.Fatal("seed workspaces")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'profile-owner@example.com', 'encoded', 'active')", userID); err != nil {
		t.Fatal("seed user")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')", workspaceID, userID); err != nil {
		t.Fatal("seed membership")
	}

	repository := NewPostgresRepository(pool)
	mockJSON, err := json.Marshal(mockConfig())
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.Create(ctx, CreateParams{
		ProfileID: mockID, AuditID: "80000000-0000-4000-8000-000000000071",
		WorkspaceID: workspaceID, CreatedBy: userID,
		ProviderID: asr.MockProviderID, DisplayName: "Mock",
		ConfigJSON: mockJSON, State: StateDisabled, Priority: 100,
	})
	if err != nil {
		t.Fatalf("Create(mock) error = %v", err)
	}
	if created.ID != mockID || created.SecretConfigured || created.Version != 1 {
		t.Fatalf("created mock = %+v", created)
	}
	if _, err := repository.Create(ctx, CreateParams{
		ProfileID:   "90000000-0000-4000-8000-000000000073",
		AuditID:     "80000000-0000-4000-8000-000000000073",
		WorkspaceID: workspaceID, CreatedBy: userID,
		ProviderID: asr.MockProviderID, DisplayName: "mock",
		ConfigJSON: mockJSON, State: StateDisabled, Priority: 101,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Create() error = %v", err)
	}

	tencentJSON, err := json.Marshal(tencentConfig())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := bytes.Repeat([]byte{0x5a}, 64)
	created, err = repository.Create(ctx, CreateParams{
		ProfileID: tencentID, AuditID: "80000000-0000-4000-8000-000000000072",
		WorkspaceID: workspaceID, CreatedBy: userID,
		ProviderID: asr.TencentProviderID, DisplayName: "Tencent",
		ConfigJSON: tencentJSON, SecretCiphertext: ciphertext,
		State: StateEnabled, Priority: 10,
	})
	if err != nil {
		t.Fatalf("Create(Tencent) error = %v", err)
	}
	if !created.SecretConfigured || created.Priority != 10 {
		t.Fatalf("created Tencent = %+v", created)
	}

	listed, err := repository.List(ctx, workspaceID)
	if err != nil || len(listed) != 2 || listed[0].ID != tencentID || listed[1].ID != mockID {
		t.Fatalf("List() = %+v, error = %v", listed, err)
	}
	other, err := repository.List(ctx, otherSpace)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-workspace List() = %+v, error = %v", other, err)
	}
	enabled, err := repository.ListEnabledASR(ctx, workspaceID)
	if err != nil || len(enabled) != 1 || enabled[0].ID != tencentID ||
		!bytes.Equal(enabled[0].SecretCiphertext, ciphertext) {
		t.Fatalf("ListEnabledASR() = %+v, error = %v", enabled, err)
	}
	stored, err := repository.GetStored(ctx, workspaceID, tencentID)
	if err != nil || !bytes.Equal(stored.SecretCiphertext, ciphertext) {
		t.Fatalf("GetStored() = %+v, error = %v", stored, err)
	}
	if _, err := repository.GetStored(ctx, otherSpace, tencentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace GetStored() error = %v", err)
	}
	updated, err := repository.Update(ctx, UpdateParams{
		ProfileID: tencentID, AuditID: "80000000-0000-4000-8000-000000000074",
		WorkspaceID: workspaceID, UpdatedBy: userID,
		DisplayName: "Tencent fallback", ConfigJSON: tencentJSON, SecretCiphertext: ciphertext,
		State: StateDisabled, Priority: 20, ExpectedVersion: 1,
	})
	if err != nil || updated.Version != 2 || updated.State != StateDisabled || updated.Priority != 20 {
		t.Fatalf("Update() = %+v, error = %v", updated, err)
	}
	if _, err := repository.Update(ctx, UpdateParams{
		ProfileID: tencentID, AuditID: "80000000-0000-4000-8000-000000000075",
		WorkspaceID: workspaceID, UpdatedBy: userID,
		DisplayName: "stale", ConfigJSON: tencentJSON, SecretCiphertext: ciphertext,
		State: StateEnabled, Priority: 10, ExpectedVersion: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}
	if err := repository.RecordHealth(ctx, tencentID, "healthy", ""); err != nil {
		t.Fatalf("RecordHealth(healthy) error = %v", err)
	}
	if err := repository.RecordHealth(ctx, tencentID, "unhealthy", asr.ErrorTransient); err != nil {
		t.Fatalf("RecordHealth(unhealthy) error = %v", err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE workspace_id = $1 AND action LIKE 'provider_profile.%'`, workspaceID).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("provider audit count = %d, error = %v", audits, err)
	}
}

func migratedProviderProfilePool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("provider_profile_test_%d", time.Now().UnixNano())
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
