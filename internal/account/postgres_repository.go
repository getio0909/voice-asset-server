package account

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) GetPasswordHash(
	ctx context.Context,
	workspaceID,
	userID string,
) (string, error) {
	var passwordHash string
	err := repository.pool.QueryRow(ctx, `
		SELECT user_account.password_hash
		FROM users AS user_account
		JOIN memberships AS membership ON membership.user_id = user_account.id
		WHERE user_account.id = $1
		  AND user_account.status = 'active'
		  AND membership.workspace_id = $2
		  AND membership.status = 'active'`, userID, workspaceID).Scan(&passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query account password: %w", err)
	}
	return passwordHash, nil
}

func (repository *PostgresRepository) ChangePassword(
	ctx context.Context,
	params ChangePasswordParams,
) (ChangePasswordResult, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return ChangePasswordResult{}, fmt.Errorf("begin password change: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var currentHash string
	err = tx.QueryRow(ctx, `
		SELECT user_account.password_hash
		FROM users AS user_account
		JOIN memberships AS membership ON membership.user_id = user_account.id
		WHERE user_account.id = $1
		  AND user_account.status = 'active'
		  AND membership.workspace_id = $2
		  AND membership.status = 'active'
		FOR UPDATE OF user_account, membership`, params.UserID, params.WorkspaceID).Scan(&currentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChangePasswordResult{}, ErrNotFound
	}
	if err != nil {
		return ChangePasswordResult{}, fmt.Errorf("lock account password: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(currentHash), []byte(params.ExpectedPasswordHash)) != 1 {
		return ChangePasswordResult{}, ErrCredentialsChanged
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET password_hash = $2,
		    updated_at = GREATEST($3, updated_at + interval '1 microsecond')
		WHERE id = $1`, params.UserID, params.NewPasswordHash, params.ChangedAt); err != nil {
		return ChangePasswordResult{}, fmt.Errorf("persist account password: %w", err)
	}
	command, err := tx.Exec(ctx, `
		UPDATE sessions
		SET revoked_at = GREATEST($2, created_at)
		WHERE user_id = $1
		  AND revoked_at IS NULL`, params.UserID, params.ChangedAt)
	if err != nil {
		return ChangePasswordResult{}, fmt.Errorf("revoke account sessions: %w", err)
	}
	revokedSessions := int(command.RowsAffected())
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'account.password_changed', 'user', $3, $4,
			jsonb_build_object('revoked_sessions', $5::integer)
		)`, params.AuditID, params.WorkspaceID, params.UserID, params.RequestID, revokedSessions); err != nil {
		return ChangePasswordResult{}, fmt.Errorf("insert password-change audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ChangePasswordResult{}, fmt.Errorf("commit password change: %w", err)
	}
	return ChangePasswordResult{RevokedSessions: revokedSessions}, nil
}
