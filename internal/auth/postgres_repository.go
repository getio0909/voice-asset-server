package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateOwner(ctx context.Context, owner NewOwner) (Principal, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Principal{}, fmt.Errorf("begin owner transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('voiceasset.initial_owner'))"); err != nil {
		return Principal{}, fmt.Errorf("lock owner bootstrap: %w", err)
	}
	var hasUsers bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users)").Scan(&hasUsers); err != nil {
		return Principal{}, fmt.Errorf("check existing users: %w", err)
	}
	if hasUsers {
		return Principal{}, ErrOwnerExists
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO workspaces (id, name) VALUES ($1, $2)",
		owner.WorkspaceID, owner.WorkspaceName,
	); err != nil {
		return Principal{}, fmt.Errorf("insert owner workspace: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, $2, $3, 'active')`,
		owner.UserID, owner.Email, owner.PasswordHash,
	); err != nil {
		return Principal{}, fmt.Errorf("insert owner user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')`,
		owner.WorkspaceID, owner.UserID,
	); err != nil {
		return Principal{}, fmt.Errorf("insert owner membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'bootstrap.owner_created', 'user', $3, '{}')`,
		owner.AuditID, owner.WorkspaceID, owner.UserID,
	); err != nil {
		return Principal{}, fmt.Errorf("insert owner audit log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Principal{}, fmt.Errorf("commit owner transaction: %w", err)
	}
	return Principal{
		UserID: owner.UserID, WorkspaceID: owner.WorkspaceID,
		Role: "owner", Email: owner.Email, Scopes: ScopesForRole("owner"),
	}, nil
}

func (r *PostgresRepository) FindLoginAccount(ctx context.Context, email string) (LoginAccount, error) {
	var account LoginAccount
	err := r.pool.QueryRow(ctx, `
		SELECT u.id::text, m.workspace_id::text, m.role, u.email, u.password_hash, u.status
		FROM users AS u
		JOIN memberships AS m ON m.user_id = u.id
		WHERE lower(u.email) = lower($1)
		  AND m.status = 'active'
		ORDER BY m.created_at, m.workspace_id
		LIMIT 1`, email).Scan(
		&account.UserID,
		&account.WorkspaceID,
		&account.Role,
		&account.Email,
		&account.PasswordHash,
		&account.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginAccount{}, ErrAccountNotFound
	}
	if err != nil {
		return LoginAccount{}, fmt.Errorf("query login account: %w", err)
	}
	return account, nil
}

func (r *PostgresRepository) CreateSession(ctx context.Context, session NewSession) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (
			id, user_id, workspace_id, token_hash, refresh_token_hash,
			device_name, expires_at, refresh_expires_at, last_seen_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, clock_timestamp())`,
		session.ID,
		session.UserID,
		session.WorkspaceID,
		session.TokenHash,
		session.RefreshTokenHash,
		session.DeviceName,
		session.ExpiresAt,
		session.RefreshExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	if err := insertSessionAudit(
		ctx, tx, session.AuditID, session.WorkspaceID, session.UserID,
		"auth.session.created", session.ID,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session creation: %w", err)
	}
	return nil
}

func (r *PostgresRepository) CreatePairingSession(
	ctx context.Context,
	params CreatePairingSessionParams,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin pairing session creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var active bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM memberships AS membership
		JOIN users AS user_account ON user_account.id = membership.user_id
		WHERE membership.workspace_id = $1
		  AND membership.user_id = $2
		  AND membership.status = 'active'
		  AND user_account.status = 'active'
		FOR UPDATE OF membership, user_account`,
		params.WorkspaceID, params.UserID,
	).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("lock pairing account: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE pairing_sessions
		SET revoked_at = GREATEST($3, created_at)
		WHERE workspace_id = $1
		  AND user_id = $2
		  AND claimed_at IS NULL
		  AND revoked_at IS NULL`,
		params.WorkspaceID, params.UserID, params.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("revoke prior pairing sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO pairing_sessions (
			id, workspace_id, user_id, secret_hash, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		params.PairingSessionID,
		params.WorkspaceID,
		params.UserID,
		params.SecretHash,
		params.ExpiresAt,
		params.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert pairing session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'auth.pairing.created', 'pairing_session', $4,
			jsonb_build_object('expires_at', $5::timestamptz, 'revoked_previous', $6::bigint)
		)`,
		params.AuditID,
		params.WorkspaceID,
		params.UserID,
		params.PairingSessionID,
		params.ExpiresAt,
		result.RowsAffected(),
	); err != nil {
		return fmt.Errorf("insert pairing creation audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pairing session creation: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ClaimPairingSession(
	ctx context.Context,
	params ClaimPairingSessionParams,
) (PairingIdentity, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PairingIdentity{}, fmt.Errorf("begin pairing claim: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	identity := PairingIdentity{SessionID: params.SessionID}
	err = tx.QueryRow(ctx, `
		SELECT pairing.user_id::text, pairing.workspace_id::text,
		       membership.role, user_account.email
		FROM pairing_sessions AS pairing
		JOIN users AS user_account ON user_account.id = pairing.user_id
		JOIN memberships AS membership
		  ON membership.workspace_id = pairing.workspace_id
		 AND membership.user_id = pairing.user_id
		WHERE pairing.id = $1
		  AND pairing.secret_hash = $2
		  AND pairing.claimed_at IS NULL
		  AND pairing.revoked_at IS NULL
		  AND pairing.expires_at > $3
		  AND user_account.status = 'active'
		  AND membership.status = 'active'
		FOR UPDATE OF pairing`,
		params.PairingSessionID, params.SecretHash, params.ClaimedAt,
	).Scan(&identity.UserID, &identity.WorkspaceID, &identity.Role, &identity.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return PairingIdentity{}, ErrPairingUnavailable
	}
	if err != nil {
		return PairingIdentity{}, fmt.Errorf("lock pairing session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (
			id, user_id, workspace_id, token_hash, refresh_token_hash,
			device_name, expires_at, refresh_expires_at, last_seen_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		params.SessionID,
		identity.UserID,
		identity.WorkspaceID,
		params.TokenHash,
		params.RefreshTokenHash,
		params.DeviceName,
		params.ExpiresAt,
		params.RefreshExpiresAt,
		params.ClaimedAt,
	); err != nil {
		return PairingIdentity{}, fmt.Errorf("insert paired session: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE pairing_sessions
		SET claimed_at = $2, claimed_session_id = $3
		WHERE id = $1
		  AND claimed_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > $2`,
		params.PairingSessionID, params.ClaimedAt, params.SessionID,
	)
	if err != nil {
		return PairingIdentity{}, fmt.Errorf("consume pairing session: %w", err)
	}
	if result.RowsAffected() != 1 {
		return PairingIdentity{}, ErrPairingUnavailable
	}
	if err := insertSessionAudit(
		ctx, tx, params.SessionAuditID, identity.WorkspaceID, identity.UserID,
		"auth.session.created", params.SessionID,
	); err != nil {
		return PairingIdentity{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'auth.pairing.claimed', 'pairing_session', $4,
			jsonb_build_object('session_id', $5::uuid)
		)`,
		params.ClaimAuditID,
		identity.WorkspaceID,
		identity.UserID,
		params.PairingSessionID,
		params.SessionID,
	); err != nil {
		return PairingIdentity{}, fmt.Errorf("insert pairing claim audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PairingIdentity{}, fmt.Errorf("commit pairing claim: %w", err)
	}
	return identity, nil
}

func (r *PostgresRepository) ResolveSession(ctx context.Context, tokenHash string, now time.Time) (Principal, error) {
	var principal Principal
	err := r.pool.QueryRow(ctx, `
		UPDATE sessions AS s
		SET last_seen_at = GREATEST(s.last_seen_at, $2, s.created_at)
		FROM users AS u, memberships AS m
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > $2
		  AND u.id = s.user_id
		  AND u.status = 'active'
		  AND m.status = 'active'
		  AND m.workspace_id = s.workspace_id
		  AND m.user_id = s.user_id
		RETURNING u.id::text, s.workspace_id::text, m.role, u.email, s.id::text`,
		tokenHash, now).Scan(
		&principal.UserID,
		&principal.WorkspaceID,
		&principal.Role,
		&principal.Email,
		&principal.CredentialID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrSessionNotFound
	}
	if err != nil {
		return Principal{}, fmt.Errorf("query session: %w", err)
	}
	principal.Scopes = ScopesForRole(principal.Role)
	principal.CredentialType = "session"
	return principal, nil
}

func (r *PostgresRepository) RotateSession(
	ctx context.Context,
	params RotateSessionParams,
) (SessionIdentity, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return SessionIdentity{}, fmt.Errorf("begin session rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var identity SessionIdentity
	err = tx.QueryRow(ctx, `
		UPDATE sessions AS s
		SET token_hash = $2,
		    refresh_token_hash = $3,
		    expires_at = LEAST($4, s.refresh_expires_at),
		    last_seen_at = GREATEST(s.last_seen_at, $5, s.created_at),
		    rotated_at = GREATEST($5, s.created_at)
		FROM users AS u, memberships AS m
		WHERE s.refresh_token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.refresh_expires_at > $5
		  AND u.id = s.user_id
		  AND u.status = 'active'
		  AND m.status = 'active'
		  AND m.workspace_id = s.workspace_id
		  AND m.user_id = s.user_id
		RETURNING s.id::text, u.id::text, s.workspace_id::text, m.role, u.email,
		          s.expires_at, s.refresh_expires_at`,
		params.CurrentRefreshTokenHash,
		params.NewTokenHash,
		params.NewRefreshTokenHash,
		params.NewExpiresAt,
		params.RotatedAt,
	).Scan(
		&identity.ID,
		&identity.Principal.UserID,
		&identity.Principal.WorkspaceID,
		&identity.Principal.Role,
		&identity.Principal.Email,
		&identity.ExpiresAt,
		&identity.RefreshExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionIdentity{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionIdentity{}, fmt.Errorf("rotate session: %w", err)
	}
	if err := insertSessionAudit(
		ctx, tx, params.AuditID, identity.Principal.WorkspaceID, identity.Principal.UserID,
		"auth.session.refreshed", identity.ID,
	); err != nil {
		return SessionIdentity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SessionIdentity{}, fmt.Errorf("commit session rotation: %w", err)
	}
	identity.Principal.Scopes = ScopesForRole(identity.Principal.Role)
	identity.Principal.CredentialType = "session"
	identity.Principal.CredentialID = identity.ID
	return identity, nil
}

func (r *PostgresRepository) ListDeviceSessions(
	ctx context.Context,
	workspaceID,
	userID string,
	now time.Time,
) ([]DeviceSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, device_name, created_at, last_seen_at, expires_at,
		       COALESCE(refresh_expires_at, expires_at), revoked_at
		FROM sessions AS s
		WHERE workspace_id = $1
		  AND user_id = $2
		  AND revoked_at IS NULL
		  AND GREATEST(expires_at, COALESCE(refresh_expires_at, expires_at)) > $3
		ORDER BY last_seen_at DESC, id`, workspaceID, userID, now)
	if err != nil {
		return nil, fmt.Errorf("query device sessions: %w", err)
	}
	defer rows.Close()
	result := make([]DeviceSession, 0)
	for rows.Next() {
		session, scanErr := scanDeviceSession(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan device session: %w", scanErr)
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate device sessions: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) RevokeDeviceSession(
	ctx context.Context,
	params RevokeDeviceSessionParams,
) (DeviceSession, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DeviceSession{}, fmt.Errorf("begin device session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	row := tx.QueryRow(ctx, `
		UPDATE sessions
		SET revoked_at = GREATEST($4, created_at)
		WHERE id = $1
		  AND workspace_id = $2
		  AND user_id = $3
		  AND revoked_at IS NULL
		RETURNING id::text, device_name, created_at, last_seen_at, expires_at,
		          COALESCE(refresh_expires_at, expires_at), revoked_at`,
		params.SessionID, params.WorkspaceID, params.UserID, params.RevokedAt,
	)
	session, err := scanDeviceSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeviceSession{}, ErrSessionNotFound
	}
	if err != nil {
		return DeviceSession{}, fmt.Errorf("revoke device session: %w", err)
	}
	if err := insertSessionAudit(
		ctx, tx, params.AuditID, params.WorkspaceID, params.UserID,
		"auth.session.revoked", session.ID,
	); err != nil {
		return DeviceSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeviceSession{}, fmt.Errorf("commit device session revocation: %w", err)
	}
	return session, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanDeviceSession(row rowScanner) (DeviceSession, error) {
	var session DeviceSession
	err := row.Scan(
		&session.ID,
		&session.DeviceName,
		&session.CreatedAt,
		&session.LastSeenAt,
		&session.ExpiresAt,
		&session.RefreshExpiresAt,
		&session.RevokedAt,
	)
	return session, err
}

func insertSessionAudit(
	ctx context.Context,
	tx pgx.Tx,
	auditID,
	workspaceID,
	userID,
	action,
	sessionID string,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', $4, 'session', $5, '{}')`,
		auditID, workspaceID, userID, action, sessionID,
	); err != nil {
		return fmt.Errorf("insert session audit: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ResolveAPIKey(ctx context.Context, tokenHash string, now time.Time) (Principal, error) {
	var principal Principal
	err := r.pool.QueryRow(ctx, `
		UPDATE api_keys AS api_key
		SET last_used_at = GREATEST($2, api_key.created_at)
		FROM users AS user_account, memberships AS membership
		WHERE api_key.token_hash = $1
		  AND api_key.revoked_at IS NULL
		  AND api_key.expires_at > $2
		  AND user_account.id = api_key.created_by
		  AND user_account.status = 'active'
		  AND membership.workspace_id = api_key.workspace_id
		  AND membership.user_id = api_key.created_by
		  AND membership.status = 'active'
		RETURNING user_account.id::text, api_key.workspace_id::text,
		          user_account.email, api_key.scopes, api_key.id::text`, tokenHash, now,
	).Scan(
		&principal.UserID,
		&principal.WorkspaceID,
		&principal.Email,
		&principal.Scopes,
		&principal.CredentialID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrSessionNotFound
	}
	if err != nil {
		return Principal{}, fmt.Errorf("query API key: %w", err)
	}
	principal.Role = "agent"
	principal.CredentialType = "api_key"
	return principal, nil
}

func (r *PostgresRepository) RevokeSession(ctx context.Context, tokenHash string, now time.Time) error {
	return r.revokeSession(ctx, tokenHash, "", now)
}

func (r *PostgresRepository) RevokeSessionWithAudit(
	ctx context.Context,
	tokenHash,
	auditID string,
	now time.Time,
) error {
	return r.revokeSession(ctx, tokenHash, auditID, now)
}

func (r *PostgresRepository) revokeSession(
	ctx context.Context,
	tokenHash,
	auditID string,
	now time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var sessionID, workspaceID, userID string
	err = tx.QueryRow(ctx, `
		UPDATE sessions
		SET revoked_at = GREATEST($2, created_at)
		WHERE token_hash = $1 AND revoked_at IS NULL
		RETURNING id::text, workspace_id::text, user_id::text`, tokenHash, now,
	).Scan(&sessionID, &workspaceID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if auditID != "" {
		if err := insertSessionAudit(
			ctx, tx, auditID, workspaceID, userID, "auth.session.revoked", sessionID,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session revocation: %w", err)
	}
	return nil
}
