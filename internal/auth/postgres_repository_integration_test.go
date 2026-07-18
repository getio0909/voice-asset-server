package auth_test

import (
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

func TestPostgresRepositorySessionLifecycle(t *testing.T) {
	pool := migratedPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	const (
		workspaceID    = "10000000-0000-4000-8000-000000000001"
		userID         = "20000000-0000-4000-8000-000000000001"
		sessionID      = "30000000-0000-4000-8000-000000000001"
		sessionAuditID = "40000000-0000-4000-8000-000000000001"
		rotateAuditID  = "40000000-0000-4000-8000-000000000002"
		revokeAuditID  = "40000000-0000-4000-8000-000000000003"
		tokenHash      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		refreshHash    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		newTokenHash   = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		newRefreshHash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Primary Workspace')", workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, 'owner@example.com', 'encoded-password', 'active')`, userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	repository := auth.NewPostgresRepository(pool)
	account, err := repository.FindLoginAccount(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("FindLoginAccount() error = %v", err)
	}
	if account.UserID != userID || account.WorkspaceID != workspaceID || account.Role != "owner" {
		t.Fatalf("unexpected account: %+v", account)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := repository.CreateSession(ctx, auth.NewSession{
		ID: sessionID, AuditID: sessionAuditID, UserID: userID, WorkspaceID: workspaceID,
		TokenHash: tokenHash, RefreshTokenHash: refreshHash, DeviceName: "Firefox on Windows",
		ExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(7 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	principal, err := repository.ResolveSession(ctx, tokenHash, now)
	if err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if principal.UserID != userID || principal.WorkspaceID != workspaceID || principal.Email != "owner@example.com" ||
		principal.CredentialID != sessionID {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	sessions, err := repository.ListDeviceSessions(ctx, workspaceID, userID, now)
	if err != nil || len(sessions) != 1 || sessions[0].ID != sessionID ||
		sessions[0].DeviceName != "Firefox on Windows" {
		t.Fatalf("ListDeviceSessions() = (%+v, %v)", sessions, err)
	}
	identity, err := repository.RotateSession(ctx, auth.RotateSessionParams{
		AuditID:                 rotateAuditID,
		CurrentRefreshTokenHash: refreshHash,
		NewTokenHash:            newTokenHash, NewRefreshTokenHash: newRefreshHash,
		NewExpiresAt: now.Add(2 * time.Hour), RotatedAt: now.Add(time.Minute),
	})
	if err != nil || identity.ID != sessionID || identity.Principal.UserID != userID ||
		!identity.ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("RotateSession() = (%+v, %v)", identity, err)
	}
	if _, err := repository.ResolveSession(ctx, tokenHash, now.Add(time.Minute)); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("old access ResolveSession() error = %v, want ErrSessionNotFound", err)
	}
	if _, err := repository.RotateSession(ctx, auth.RotateSessionParams{
		AuditID:                 "40000000-0000-4000-8000-000000000004",
		CurrentRefreshTokenHash: refreshHash,
		NewTokenHash:            tokenHash, NewRefreshTokenHash: refreshHash,
		NewExpiresAt: now.Add(2 * time.Hour), RotatedAt: now.Add(2 * time.Minute),
	}); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("replayed RotateSession() error = %v, want ErrSessionNotFound", err)
	}
	if _, err := repository.ResolveSession(ctx, newTokenHash, now.Add(time.Minute)); err != nil {
		t.Fatalf("new access ResolveSession() error = %v", err)
	}
	revoked, err := repository.RevokeDeviceSession(ctx, auth.RevokeDeviceSessionParams{
		AuditID: revokeAuditID, WorkspaceID: workspaceID, UserID: userID,
		SessionID: sessionID, RevokedAt: now.Add(3 * time.Minute),
	})
	if err != nil || revoked.ID != sessionID || revoked.RevokedAt == nil {
		t.Fatalf("RevokeDeviceSession() = (%+v, %v)", revoked, err)
	}
	if _, err := repository.ResolveSession(ctx, newTokenHash, now.Add(4*time.Minute)); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("revoked ResolveSession() error = %v, want ErrSessionNotFound", err)
	}
	if sessions, err := repository.ListDeviceSessions(ctx, workspaceID, userID, now.Add(4*time.Minute)); err != nil || len(sessions) != 0 {
		t.Fatalf("post-revoke ListDeviceSessions() = (%+v, %v)", sessions, err)
	}
	var lifecycleAudits int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_logs
		WHERE target_id = $1
		  AND action IN ('auth.session.created', 'auth.session.refreshed', 'auth.session.revoked')`,
		sessionID,
	).Scan(&lifecycleAudits); err != nil || lifecycleAudits != 3 {
		t.Fatalf("session lifecycle audit count = %d, error = %v", lifecycleAudits, err)
	}
	if _, err := repository.ResolveSession(ctx, tokenHash, now.Add(2*time.Hour)); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("expired ResolveSession() error = %v, want ErrSessionNotFound", err)
	}
	if _, err := repository.FindLoginAccount(ctx, "missing@example.com"); !errors.Is(err, auth.ErrAccountNotFound) {
		t.Fatalf("missing FindLoginAccount() error = %v, want ErrAccountNotFound", err)
	}
}

func TestPostgresRepositoryPairingSessionIsNewestOnlyAtomicAndOneTime(t *testing.T) {
	pool := migratedPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "11000000-0000-4000-8000-000000000001"
		userID      = "21000000-0000-4000-8000-000000000001"
		firstID     = "31000000-0000-4000-8000-000000000001"
		secondID    = "31000000-0000-4000-8000-000000000002"
		firstHash   = "1111111111111111111111111111111111111111111111111111111111111111"
		secondHash  = "2222222222222222222222222222222222222222222222222222222222222222"
		tokenHash   = "3333333333333333333333333333333333333333333333333333333333333333"
		refreshHash = "4444444444444444444444444444444444444444444444444444444444444444"
		sessionID   = "51000000-0000-4000-8000-000000000001"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Pairing Workspace')", workspaceID); err != nil {
		t.Fatalf("seed pairing workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, 'pairing@example.com', 'encoded-password', 'active')`, userID); err != nil {
		t.Fatalf("seed pairing user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $2, 'editor')`, workspaceID, userID); err != nil {
		t.Fatalf("seed pairing membership: %v", err)
	}
	repository := auth.NewPostgresRepository(pool)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := repository.CreatePairingSession(ctx, auth.CreatePairingSessionParams{
		PairingSessionID: firstID,
		AuditID:          "41000000-0000-4000-8000-000000000001",
		WorkspaceID:      workspaceID,
		UserID:           userID,
		SecretHash:       firstHash,
		CreatedAt:        createdAt,
		ExpiresAt:        createdAt.Add(5 * time.Minute),
	}); err != nil {
		t.Fatalf("CreatePairingSession(first) error = %v", err)
	}
	if err := repository.CreatePairingSession(ctx, auth.CreatePairingSessionParams{
		PairingSessionID: secondID,
		AuditID:          "41000000-0000-4000-8000-000000000002",
		WorkspaceID:      workspaceID,
		UserID:           userID,
		SecretHash:       secondHash,
		CreatedAt:        createdAt.Add(time.Second),
		ExpiresAt:        createdAt.Add(5*time.Minute + time.Second),
	}); err != nil {
		t.Fatalf("CreatePairingSession(second) error = %v", err)
	}
	var firstRevoked bool
	if err := pool.QueryRow(ctx, `
		SELECT revoked_at IS NOT NULL FROM pairing_sessions WHERE id = $1`, firstID,
	).Scan(&firstRevoked); err != nil || !firstRevoked {
		t.Fatalf("first pairing revoked = %t, error = %v", firstRevoked, err)
	}
	claim := auth.ClaimPairingSessionParams{
		PairingSessionID: secondID,
		SessionID:        sessionID,
		SessionAuditID:   "41000000-0000-4000-8000-000000000003",
		ClaimAuditID:     "41000000-0000-4000-8000-000000000004",
		SecretHash:       secondHash,
		TokenHash:        tokenHash,
		RefreshTokenHash: refreshHash,
		DeviceName:       "Pixel 9 Pro",
		ClaimedAt:        createdAt.Add(2 * time.Second),
		ExpiresAt:        createdAt.Add(12 * time.Hour),
		RefreshExpiresAt: createdAt.Add(30 * 24 * time.Hour),
	}
	if _, err := repository.ClaimPairingSession(ctx, auth.ClaimPairingSessionParams{
		PairingSessionID: firstID, SecretHash: firstHash, SessionID: "51000000-0000-4000-8000-000000000002",
		SessionAuditID: "41000000-0000-4000-8000-000000000005", ClaimAuditID: "41000000-0000-4000-8000-000000000006",
		TokenHash: "5555555555555555555555555555555555555555555555555555555555555555", RefreshTokenHash: "6666666666666666666666666666666666666666666666666666666666666666",
		DeviceName: "Revoked code", ClaimedAt: claim.ClaimedAt, ExpiresAt: claim.ExpiresAt, RefreshExpiresAt: claim.RefreshExpiresAt,
	}); !errors.Is(err, auth.ErrPairingUnavailable) {
		t.Fatalf("ClaimPairingSession(revoked) error = %v, want ErrPairingUnavailable", err)
	}
	identity, err := repository.ClaimPairingSession(ctx, claim)
	if err != nil {
		t.Fatalf("ClaimPairingSession() error = %v", err)
	}
	if identity.SessionID != sessionID || identity.UserID != userID || identity.WorkspaceID != workspaceID ||
		identity.Role != "editor" || identity.Email != "pairing@example.com" {
		t.Fatalf("pairing identity = %+v", identity)
	}
	if _, err := repository.ClaimPairingSession(ctx, claim); !errors.Is(err, auth.ErrPairingUnavailable) {
		t.Fatalf("ClaimPairingSession(replay) error = %v, want ErrPairingUnavailable", err)
	}
	principal, err := repository.ResolveSession(ctx, tokenHash, claim.ClaimedAt)
	if err != nil || principal.CredentialID != sessionID || principal.UserID != userID {
		t.Fatalf("ResolveSession(paired) = (%+v, %v)", principal, err)
	}
	var storedHash string
	var claimedSessionID string
	if err := pool.QueryRow(ctx, `
		SELECT secret_hash, claimed_session_id::text FROM pairing_sessions WHERE id = $1`, secondID,
	).Scan(&storedHash, &claimedSessionID); err != nil || storedHash != secondHash || claimedSessionID != sessionID {
		t.Fatalf("stored pairing = hash_match:%t session:%q error:%v", storedHash == secondHash, claimedSessionID, err)
	}
	var auditCount int
	var auditContainsCredential bool
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(bool_or(metadata::text LIKE '%' || $1 || '%'), false)
		FROM audit_logs
		WHERE workspace_id = $2
		  AND action IN ('auth.pairing.created', 'auth.pairing.claimed', 'auth.session.created')`,
		secondHash, workspaceID,
	).Scan(&auditCount, &auditContainsCredential); err != nil || auditCount != 4 || auditContainsCredential {
		t.Fatalf("pairing audits = count:%d contains_credential:%t error:%v", auditCount, auditContainsCredential, err)
	}
}

func TestPostgresRepositoryCreatesOnlyInitialOwner(t *testing.T) {
	pool := migratedPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	repository := auth.NewPostgresRepository(pool)
	owner := auth.NewOwner{
		UserID:        "20000000-0000-4000-8000-000000000002",
		WorkspaceID:   "10000000-0000-4000-8000-000000000002",
		AuditID:       "40000000-0000-4000-8000-000000000002",
		Email:         "owner@example.com",
		PasswordHash:  "encoded-password",
		WorkspaceName: "Primary Workspace",
	}

	principal, err := repository.CreateOwner(ctx, owner)
	if err != nil {
		t.Fatalf("CreateOwner() error = %v", err)
	}
	if principal.Role != "owner" || principal.WorkspaceID != owner.WorkspaceID {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	owner.UserID = "20000000-0000-4000-8000-000000000003"
	owner.WorkspaceID = "10000000-0000-4000-8000-000000000003"
	owner.AuditID = "40000000-0000-4000-8000-000000000003"
	owner.Email = "another@example.com"
	if _, err := repository.CreateOwner(ctx, owner); !errors.Is(err, auth.ErrOwnerExists) {
		t.Fatalf("second CreateOwner() error = %v, want ErrOwnerExists", err)
	}

	var users, workspaces, audits int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM users),
		       (SELECT count(*) FROM workspaces),
		       (SELECT count(*) FROM audit_logs)`).Scan(&users, &workspaces, &audits); err != nil {
		t.Fatalf("count bootstrap records: %v", err)
	}
	if users != 1 || workspaces != 1 || audits != 1 {
		t.Fatalf("counts = users:%d workspaces:%d audits:%d, want 1 each", users, workspaces, audits)
	}
}

func migratedPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("auth_test_%d", time.Now().UnixNano())
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
