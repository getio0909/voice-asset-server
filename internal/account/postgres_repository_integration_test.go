package account

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPasswordChangeIsAtomicAndRevokesEveryUserSession(t *testing.T) {
	pool := migratedAccountPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	const (
		otherWorkspaceID = "10000000-0000-4000-8000-000000000002"
		otherSessionID   = "30000000-0000-4000-8000-000000000099"
		duplicateAuditID = "40000000-0000-4000-8000-000000000001"
	)
	hasher := auth.PasswordHasher{Iterations: 1_000}
	oldHash, err := hasher.Hash("current-password")
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	futureUpdatedAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	seedStatements := []struct {
		query string
		args  []any
	}{
		{query: "INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", args: []any{testWorkspaceID, otherWorkspaceID}},
		{query: `INSERT INTO users (id, email, password_hash, status, updated_at)
			VALUES ($1, 'owner@example.com', $2, 'active', $3)`, args: []any{testUserID, oldHash, futureUpdatedAt}},
		{query: `INSERT INTO memberships (workspace_id, user_id, role)
			VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`, args: []any{testWorkspaceID, otherWorkspaceID, testUserID}},
		{query: `INSERT INTO audit_logs (
				id, workspace_id, actor_id, actor_type, action, target_type, target_id
			) VALUES ($1, $2, $3, 'user', 'test.audit', 'user', $3)`, args: []any{duplicateAuditID, testWorkspaceID, testUserID}},
	}
	for _, statement := range seedStatements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed account: %v", err)
		}
	}

	authRepository := auth.NewPostgresRepository(pool)
	authService := auth.NewService(authRepository, hasher)
	first, err := authService.LoginWithDevice(ctx, "owner@example.com", "current-password", "First browser")
	if err != nil {
		t.Fatalf("first LoginWithDevice() error = %v", err)
	}
	second, err := authService.LoginWithDevice(ctx, "owner@example.com", "current-password", "Second browser")
	if err != nil {
		t.Fatalf("second LoginWithDevice() error = %v", err)
	}
	if err := authRepository.CreateSession(ctx, auth.NewSession{
		ID: otherSessionID, AuditID: "40000000-0000-4000-8000-000000000002",
		UserID: testUserID, WorkspaceID: otherWorkspaceID,
		TokenHash: strings.Repeat("a", 64), RefreshTokenHash: strings.Repeat("b", 64),
		DeviceName: "Other workspace", ExpiresAt: time.Now().Add(time.Hour),
		RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create cross-workspace session: %v", err)
	}

	repository := NewPostgresRepository(pool)
	rollbackHash, err := hasher.Hash("rollback-password")
	if err != nil {
		t.Fatalf("hash rollback password: %v", err)
	}
	if _, err := repository.ChangePassword(ctx, ChangePasswordParams{
		WorkspaceID: testWorkspaceID, UserID: testUserID, AuditID: duplicateAuditID,
		RequestID: "rollback-request", ExpectedPasswordHash: oldHash,
		NewPasswordHash: rollbackHash, ChangedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("ChangePassword() with duplicate audit ID succeeded, want rollback")
	}
	assertPasswordAndActiveSessions(t, ctx, pool, oldHash, 3)
	if _, err := repository.ChangePassword(ctx, ChangePasswordParams{
		WorkspaceID: testWorkspaceID, UserID: testUserID,
		ExpectedPasswordHash: "stale-hash", NewPasswordHash: rollbackHash,
	}); !errors.Is(err, ErrCredentialsChanged) {
		t.Fatalf("stale ChangePassword() error = %v, want ErrCredentialsChanged", err)
	}
	assertPasswordAndActiveSessions(t, ctx, pool, oldHash, 3)

	service := NewService(repository, auth.PasswordHasher{
		Iterations: 1_000, Random: bytes.NewReader(bytes.Repeat([]byte{0x77}, 16)),
	})
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x88}, 16))
	service.now = func() time.Time { return futureUpdatedAt.Add(-2 * time.Hour) }
	if _, err := service.ChangePassword(ctx, first.User, ChangePasswordInput{
		CurrentPassword: "wrong-password", NewPassword: "new-password-456",
	}, "wrong-request"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong-current ChangePassword() error = %v", err)
	}
	assertPasswordAndActiveSessions(t, ctx, pool, oldHash, 3)

	result, err := service.ChangePassword(ctx, first.User, ChangePasswordInput{
		CurrentPassword: "current-password", NewPassword: "new-password-456",
	}, "password-request")
	if err != nil || result.RevokedSessions != 3 {
		t.Fatalf("ChangePassword() = (%+v, %v)", result, err)
	}
	for name, token := range map[string]string{"first": first.AccessToken, "second": second.AccessToken} {
		if _, err := authService.Authenticate(ctx, token); !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatalf("%s old session Authenticate() error = %v, want unauthorized", name, err)
		}
	}
	for name, token := range map[string]string{"first": first.RefreshToken, "second": second.RefreshToken} {
		if _, err := authService.Refresh(ctx, token); !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatalf("%s old refresh Refresh() error = %v, want unauthorized", name, err)
		}
	}
	if _, err := authService.Login(ctx, "owner@example.com", "current-password"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("old-password Login() error = %v, want invalid credentials", err)
	}
	if _, err := authService.Login(ctx, "owner@example.com", "new-password-456"); err != nil {
		t.Fatalf("new-password Login() error = %v", err)
	}

	var storedHash, metadata, requestID string
	var updatedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT user_account.password_hash, user_account.updated_at,
		       audit.metadata::text, audit.request_id
		FROM users AS user_account
		JOIN audit_logs AS audit ON audit.target_id = user_account.id
		WHERE user_account.id = $1 AND audit.action = 'account.password_changed'`, testUserID,
	).Scan(&storedHash, &updatedAt, &metadata, &requestID); err != nil {
		t.Fatalf("query password-change state: %v", err)
	}
	matched, err := hasher.Verify(storedHash, "new-password-456")
	if err != nil || !matched || !updatedAt.After(futureUpdatedAt) {
		t.Fatalf("stored password/updated_at = matched:%t error:%v updated:%s", matched, err, updatedAt)
	}
	if metadata != `{"revoked_sessions": 3}` || requestID != "password-request" ||
		strings.Contains(metadata, "password") || strings.Contains(metadata, "pbkdf2") {
		t.Fatalf("audit metadata/request = %s/%s", metadata, requestID)
	}
}

func assertPasswordAndActiveSessions(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	wantHash string,
	wantActive int,
) {
	t.Helper()
	var storedHash string
	var active int
	if err := pool.QueryRow(ctx, `
		SELECT password_hash,
		       (SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL)
		FROM users WHERE id = $1`, testUserID).Scan(&storedHash, &active); err != nil {
		t.Fatalf("query password/session state: %v", err)
	}
	if storedHash != wantHash || active != wantActive {
		t.Fatalf("password/session state = hash match:%t active:%d, want active:%d", storedHash == wantHash, active, wantActive)
	}
}

func migratedAccountPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
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
	schema := fmt.Sprintf("account_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
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
