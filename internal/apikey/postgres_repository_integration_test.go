package apikey_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/apikey"
	"github.com/getio0909/voice-asset-server/internal/audit"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAPIKeyLifecycleIsScopedHashedAndAudited(t *testing.T) {
	pool := migratedAPIKeyPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "10000000-0000-4000-8000-000000000041"
		otherSpace  = "10000000-0000-4000-8000-000000000042"
		userID      = "20000000-0000-4000-8000-000000000041"
	)
	if _, err := pool.Exec(ctx,
		"INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')",
		workspaceID, otherSpace,
	); err != nil {
		t.Fatalf("seed workspaces: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, 'owner@example.com', 'encoded', 'active')`, userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`, workspaceID, otherSpace, userID); err != nil {
		t.Fatalf("seed memberships: %v", err)
	}

	repository := apikey.NewPostgresRepository(pool)
	service := apikey.NewService(repository)
	owner := auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Role: "owner", Email: "owner@example.com",
		Scopes: auth.AllScopes(), CredentialType: "session",
	}
	created, err := service.Create(ctx, owner, apikey.CreateInput{
		Name: "MCP reader", Scopes: []string{auth.ScopeTranscriptsRead, auth.ScopeAssetsRead},
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, "api-key-create-integration")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Token == "" || created.APIKey.ID == "" || created.APIKey.TokenPrefix == "" {
		t.Fatalf("Create() returned incomplete credential metadata: %+v", created.APIKey)
	}

	var storedHash, storedPrefix string
	if err := pool.QueryRow(ctx, `
		SELECT token_hash, token_prefix FROM api_keys WHERE id = $1`, created.APIKey.ID,
	).Scan(&storedHash, &storedPrefix); err != nil {
		t.Fatalf("query stored credential: %v", err)
	}
	digest := sha256.Sum256([]byte(created.Token))
	if storedHash != hex.EncodeToString(digest[:]) || storedHash == created.Token {
		t.Fatal("database did not retain only the expected token digest")
	}
	if storedPrefix != created.APIKey.TokenPrefix || storedPrefix == created.Token {
		t.Fatalf("stored display prefix = %q", storedPrefix)
	}

	authService := auth.NewService(auth.NewPostgresRepository(pool), auth.PasswordHasher{Iterations: 1_000})
	agent, err := authService.Authenticate(ctx, created.Token)
	if err != nil {
		t.Fatalf("Authenticate(API key) error = %v", err)
	}
	if agent.Role != "agent" || agent.CredentialType != "api_key" || agent.CredentialID != created.APIKey.ID {
		t.Fatalf("API key principal = %+v", agent)
	}
	if !reflect.DeepEqual(agent.Scopes, []string{auth.ScopeAssetsRead, auth.ScopeTranscriptsRead}) {
		t.Fatalf("API key scopes = %v", agent.Scopes)
	}
	if !agent.Can(auth.ScopeAssetsRead) || agent.Can(auth.ScopeAdminWrite) {
		t.Fatalf("unexpected effective permissions: %v", agent.Scopes)
	}
	var lastUsedAt *time.Time
	if err := pool.QueryRow(ctx, "SELECT last_used_at FROM api_keys WHERE id = $1", created.APIKey.ID).Scan(&lastUsedAt); err != nil || lastUsedAt == nil {
		t.Fatalf("last_used_at = (%v, %v)", lastUsedAt, err)
	}

	auditService := audit.NewService(audit.NewPostgresRepository(pool))
	if err := auditService.Record(ctx, audit.RecordInput{
		Principal: agent, Action: "asset.list", TargetType: "asset",
		RequestID: "api-key-read-integration",
	}); err != nil {
		t.Fatalf("Record(agent read) error = %v", err)
	}
	var actorType, auditedKeyID string
	if err := pool.QueryRow(ctx, `
		SELECT actor_type, metadata->>'api_key_id'
		FROM audit_logs WHERE request_id = 'api-key-read-integration'`,
	).Scan(&actorType, &auditedKeyID); err != nil {
		t.Fatalf("query agent audit: %v", err)
	}
	if actorType != "agent" || auditedKeyID != created.APIKey.ID {
		t.Fatalf("agent audit = actor:%q key:%q", actorType, auditedKeyID)
	}

	listed, err := service.List(ctx, owner)
	if err != nil || len(listed) != 1 || listed[0].TokenPrefix != storedPrefix {
		t.Fatalf("List() = (%+v, %v)", listed, err)
	}
	otherWorkspaceOwner := owner
	otherWorkspaceOwner.WorkspaceID = otherSpace
	if _, err := service.Revoke(ctx, otherWorkspaceOwner, created.APIKey.ID, "cross-workspace-revoke"); !errors.Is(err, apikey.ErrNotFound) {
		t.Fatalf("cross-workspace Revoke() error = %v, want ErrNotFound", err)
	}
	if _, err := authService.Authenticate(ctx, created.Token); err != nil {
		t.Fatalf("cross-workspace attempt changed credential: %v", err)
	}

	if _, err := service.Revoke(ctx, owner, created.APIKey.ID, "api-key-revoke-integration"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := service.Revoke(ctx, owner, created.APIKey.ID, "api-key-revoke-replay"); err != nil {
		t.Fatalf("idempotent Revoke() error = %v", err)
	}
	if _, err := authService.Authenticate(ctx, created.Token); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("Authenticate(revoked API key) error = %v, want ErrUnauthorized", err)
	}
	var createdAudits, revokedAudits int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE action = 'api_key.created'),
		       count(*) FILTER (WHERE action = 'api_key.revoked')
		FROM audit_logs WHERE target_id = $1`, created.APIKey.ID,
	).Scan(&createdAudits, &revokedAudits); err != nil {
		t.Fatalf("count API key audits: %v", err)
	}
	if createdAudits != 1 || revokedAudits != 1 {
		t.Fatalf("API key audit counts = created:%d revoked:%d", createdAudits, revokedAudits)
	}
}

func migratedAPIKeyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("api_key_test_%d", time.Now().UnixNano())
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
		t.Fatalf("parse database configuration: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire migration connection: %v", err)
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
