package membership

import (
	"bytes"
	"context"
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
	membershipWorkspace      = "11000000-0000-4000-8000-000000000001"
	membershipOtherWorkspace = "11000000-0000-4000-8000-000000000002"
	membershipOwner          = "21000000-0000-4000-8000-000000000001"
	membershipSecondOwner    = "21000000-0000-4000-8000-000000000002"
	membershipOtherUser      = "21000000-0000-4000-8000-000000000003"
)

func TestPostgresMembershipLifecycleIsScopedAuditedAndRevokesCredentials(t *testing.T) {
	pool := migratedMembershipPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	seedMembershipFixtures(t, ctx, pool)

	repository := NewPostgresRepository(pool)
	service := NewService(repository)
	service.hasher = auth.PasswordHasher{Iterations: 1, Random: bytes.NewReader(bytes.Repeat([]byte{0x11}, 16))}
	deterministicRandom := make([]byte, 160)
	for index := range deterministicRandom {
		deterministicRandom[index] = byte(index)
	}
	service.random = bytes.NewReader(deterministicRandom)
	service.now = func() time.Time { return time.Date(2026, 7, 17, 15, 0, 0, 0, time.UTC) }
	principal := auth.Principal{
		UserID: membershipOwner, WorkspaceID: membershipWorkspace, Role: "owner", Scopes: auth.AllScopes(),
	}
	created, err := service.Create(ctx, principal, CreateInput{
		Email: "new.member@example.test", Password: "long-test-password", Role: "viewer",
	}, "membership-create")
	if err != nil || created.Role != "viewer" || created.Status != "active" || created.Version != 1 {
		t.Fatalf("Create() = (%+v, %v)", created, err)
	}
	var passwordHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, created.ID).Scan(&passwordHash); err != nil {
		t.Fatalf("query member password hash: %v", err)
	}
	if passwordHash == "long-test-password" {
		t.Fatal("member password was stored in plaintext")
	}
	if valid, err := (auth.PasswordHasher{}).Verify(passwordHash, "long-test-password"); err != nil || !valid {
		t.Fatalf("verify stored member password = (%v, %v)", valid, err)
	}

	listed, err := service.List(ctx, principal, ListInput{Role: "viewer", Status: "active"})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("List() = (%+v, %v)", listed, err)
	}
	updated, err := service.Update(ctx, principal, created.ID, 1, UpdateInput{Role: stringPointer("editor")}, "membership-role")
	if err != nil || updated.Role != "editor" || updated.Version != 2 {
		t.Fatalf("Update(role) = (%+v, %v)", updated, err)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("role update timestamp = %s, created = %s", updated.UpdatedAt, created.UpdatedAt)
	}
	roleUpdatedAt := updated.UpdatedAt
	if _, err := service.Update(ctx, principal, created.ID, 1, UpdateInput{Role: stringPointer("admin")}, "stale"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Update(stale) error = %v", err)
	}
	if _, err := service.Update(ctx, principal, membershipOtherUser, 1, UpdateInput{Role: stringPointer("viewer")}, "cross-workspace"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(cross workspace) error = %v", err)
	}

	authService := auth.NewService(auth.NewPostgresRepository(pool), auth.PasswordHasher{
		Iterations: 1, Random: bytes.NewReader(bytes.Repeat([]byte{0x13}, 32)),
	})
	login, err := authService.Login(ctx, "new.member@example.test", "long-test-password")
	if err != nil || login.User.UserID != created.ID {
		t.Fatalf("Login(active member) = (%+v, %v)", login.User, err)
	}
	seedMemberAPIKey(t, ctx, pool, created.ID)
	updated, err = service.Update(ctx, principal, created.ID, 2, UpdateInput{Status: stringPointer("disabled")}, "membership-disable")
	if err != nil || updated.Status != "disabled" || updated.Version != 3 {
		t.Fatalf("Update(disable) = (%+v, %v)", updated, err)
	}
	if !updated.UpdatedAt.After(roleUpdatedAt) {
		t.Fatalf("disable timestamp = %s, role update = %s", updated.UpdatedAt, roleUpdatedAt)
	}
	if _, err := authService.Login(ctx, "new.member@example.test", "long-test-password"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("Login(disabled member) error = %v", err)
	}
	var activeSessions, activeKeys int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE workspace_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		membershipWorkspace, created.ID).Scan(&activeSessions); err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE workspace_id = $1 AND created_by = $2 AND revoked_at IS NULL`,
		membershipWorkspace, created.ID).Scan(&activeKeys); err != nil {
		t.Fatalf("count active API keys: %v", err)
	}
	if activeSessions != 0 || activeKeys != 0 {
		t.Fatalf("disabled member credentials remain active: sessions=%d keys=%d", activeSessions, activeKeys)
	}
	reactivated, err := service.Update(ctx, principal, created.ID, 3, UpdateInput{Status: stringPointer("active")}, "membership-reactivate")
	if err != nil || reactivated.Status != "active" || reactivated.Version != 4 {
		t.Fatalf("Update(reactivate) = (%+v, %v)", reactivated, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE workspace_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		membershipWorkspace, created.ID).Scan(&activeSessions); err != nil {
		t.Fatalf("count sessions after reactivation: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE workspace_id = $1 AND created_by = $2 AND revoked_at IS NULL`,
		membershipWorkspace, created.ID).Scan(&activeKeys); err != nil {
		t.Fatalf("count API keys after reactivation: %v", err)
	}
	if activeSessions != 0 || activeKeys != 0 {
		t.Fatalf("reactivation revived credentials: sessions=%d keys=%d", activeSessions, activeKeys)
	}
	if _, err := authService.Login(ctx, "new.member@example.test", "long-test-password"); err != nil {
		t.Fatalf("Login(reactivated member) error = %v", err)
	}

	if _, err := service.Update(ctx, principal, membershipSecondOwner, 1, UpdateInput{Status: stringPointer("disabled")}, "disable-second-owner"); err != nil {
		t.Fatalf("disable second owner: %v", err)
	}
	if _, err := service.Update(ctx, principal, membershipOwner, 1, UpdateInput{Status: stringPointer("disabled")}, "disable-last-owner"); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("disable last owner error = %v", err)
	}
	var createdAudits, updatedAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'membership.created'`, membershipWorkspace).Scan(&createdAudits); err != nil {
		t.Fatalf("count creation audits: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'membership.updated'`, membershipWorkspace).Scan(&updatedAudits); err != nil {
		t.Fatalf("count update audits: %v", err)
	}
	if createdAudits != 1 || updatedAudits != 4 {
		t.Fatalf("membership audits = created:%d updated:%d", createdAudits, updatedAudits)
	}
}

func seedMembershipFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name) VALUES ($1, 'Membership'), ($2, 'Other');
		INSERT INTO users (id, email, password_hash, status) VALUES
			($3, 'membership.owner@example.test', 'hash', 'active'),
			($4, 'membership.second@example.test', 'hash', 'active'),
			($5, 'membership.other@example.test', 'hash', 'active');
		INSERT INTO memberships (workspace_id, user_id, role) VALUES
			($1, $3, 'owner'), ($1, $4, 'owner'), ($2, $5, 'owner');`,
		membershipWorkspace, membershipOtherWorkspace, membershipOwner, membershipSecondOwner, membershipOtherUser)
	if err != nil {
		t.Fatalf("seed membership fixtures: %v", err)
	}
}

func seedMemberAPIKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO api_keys (
			id, workspace_id, created_by, name, token_prefix, token_hash, scopes, expires_at
		) VALUES (
			'31000000-0000-4000-8000-000000000001', $1, $2, 'member key', 'va_pat_12345678',
			repeat('a', 64), ARRAY['assets:read'], clock_timestamp() + interval '1 day'
		)`, membershipWorkspace, userID)
	if err != nil {
		t.Fatalf("seed member API key: %v", err)
	}
}

func stringPointer(value string) *string { return &value }

func migratedMembershipPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("membership_test_%d", time.Now().UnixNano())
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
