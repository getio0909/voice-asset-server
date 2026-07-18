package membership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Create(ctx context.Context, params CreateParams) (Member, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Member{}, fmt.Errorf("begin member creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockWorkspace(ctx, tx, params.WorkspaceID); err != nil {
		return Member{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, $2, $3, 'active')`, params.UserID, params.Email, params.PasswordHash); err != nil {
		if uniqueViolation(err) {
			return Member{}, ErrConflict
		}
		return Member{}, fmt.Errorf("insert member user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role, status)
		VALUES ($1, $2, $3, 'active')`, params.WorkspaceID, params.UserID, params.Role); err != nil {
		if uniqueViolation(err) {
			return Member{}, ErrConflict
		}
		return Member{}, fmt.Errorf("insert member assignment: %w", err)
	}
	created, err := scanMember(tx.QueryRow(ctx, memberSelect+`
		WHERE membership.workspace_id = $1 AND membership.user_id = $2`, params.WorkspaceID, params.UserID))
	if err != nil {
		return Member{}, fmt.Errorf("read created member: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'membership.created', 'membership', $4, $5,
			jsonb_build_object('role', $6::text, 'status', 'active')
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.UserID, params.RequestID, params.Role); err != nil {
		return Member{}, fmt.Errorf("insert member creation audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Member{}, fmt.Errorf("commit member creation: %w", err)
	}
	return created, nil
}

func (repository *PostgresRepository) List(ctx context.Context, params ListParams) ([]Member, error) {
	rows, err := repository.pool.Query(ctx, memberSelect+`
		WHERE membership.workspace_id = $1
		  AND ($2 = '' OR membership.role = $2)
		  AND ($3 = '' OR membership.status = $3)
		  AND (
			$4::timestamptz IS NULL
			OR (membership.updated_at, membership.user_id) < ($4, $5::uuid)
		  )
		ORDER BY membership.updated_at DESC, membership.user_id DESC
		LIMIT $6`, params.WorkspaceID, params.Role, params.Status,
		params.BeforeUpdatedAt, nullableUUID(params.BeforeUpdatedAt, params.BeforeID), params.Limit)
	if err != nil {
		return nil, fmt.Errorf("query workspace members: %w", err)
	}
	defer rows.Close()
	results := make([]Member, 0)
	for rows.Next() {
		member, scanErr := scanMember(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan workspace member: %w", scanErr)
		}
		results = append(results, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace members: %w", err)
	}
	return results, nil
}

func (repository *PostgresRepository) Update(ctx context.Context, params UpdateParams) (Member, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Member{}, fmt.Errorf("begin member update: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockWorkspace(ctx, tx, params.WorkspaceID); err != nil {
		return Member{}, err
	}
	current, err := scanMember(tx.QueryRow(ctx, memberSelect+`
		WHERE membership.workspace_id = $1 AND membership.user_id = $2
		FOR UPDATE OF membership`, params.WorkspaceID, params.UserID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	if err != nil {
		return Member{}, fmt.Errorf("lock workspace member: %w", err)
	}
	if current.Version != params.ExpectedVersion {
		return Member{}, ErrVersionConflict
	}
	role, status := current.Role, current.Status
	if params.Role != nil {
		role = *params.Role
	}
	if params.Status != nil {
		status = *params.Status
	}
	if role == current.Role && status == current.Status {
		if err := tx.Commit(ctx); err != nil {
			return Member{}, fmt.Errorf("commit unchanged member: %w", err)
		}
		return current, nil
	}
	if current.Role == "owner" && current.Status == "active" && (role != "owner" || status != "active") {
		var activeOwners int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM memberships
			WHERE workspace_id = $1 AND role = 'owner' AND status = 'active'`, params.WorkspaceID).Scan(&activeOwners); err != nil {
			return Member{}, fmt.Errorf("count active workspace owners: %w", err)
		}
		if activeOwners <= 1 {
			return Member{}, ErrLastOwner
		}
	}
	updated, err := scanMember(tx.QueryRow(ctx, `
		UPDATE memberships AS membership
		SET role = $3, status = $4, version = membership.version + 1,
		    updated_at = GREATEST($5, membership.updated_at + interval '1 microsecond')
		FROM users AS user_account
		WHERE membership.workspace_id = $1 AND membership.user_id = $2
		  AND user_account.id = membership.user_id
		RETURNING user_account.id::text, membership.workspace_id::text,
		          user_account.email, membership.role, membership.status,
		          membership.version, membership.created_at, membership.updated_at`,
		params.WorkspaceID, params.UserID, role, status, params.UpdatedAt))
	if err != nil {
		return Member{}, fmt.Errorf("persist member update: %w", err)
	}
	if current.Status == "active" && status == "disabled" {
		if _, err := tx.Exec(ctx, `
			UPDATE sessions SET revoked_at = GREATEST($3, created_at)
			WHERE workspace_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			params.WorkspaceID, params.UserID, params.UpdatedAt); err != nil {
			return Member{}, fmt.Errorf("revoke disabled member sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE api_keys SET revoked_at = GREATEST($3, created_at)
			WHERE workspace_id = $1 AND created_by = $2 AND revoked_at IS NULL`,
			params.WorkspaceID, params.UserID, params.UpdatedAt); err != nil {
			return Member{}, fmt.Errorf("revoke disabled member API keys: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'membership.updated', 'membership', $4, $5,
			jsonb_build_object(
				'previous_role', $6::text, 'role', $7::text,
				'previous_status', $8::text, 'status', $9::text,
				'previous_version', $10::bigint, 'version', $11::bigint
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.UserID, params.RequestID,
		current.Role, updated.Role, current.Status, updated.Status, current.Version, updated.Version); err != nil {
		return Member{}, fmt.Errorf("insert member update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Member{}, fmt.Errorf("commit member update: %w", err)
	}
	return updated, nil
}

func lockWorkspace(ctx context.Context, tx pgx.Tx, workspaceID string) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE id = $1 FOR UPDATE`, workspaceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	return nil
}

func nullableUUID(at *time.Time, id string) any {
	if at == nil {
		return nil
	}
	return id
}

func uniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

const memberSelect = `
	SELECT user_account.id::text, membership.workspace_id::text,
	       user_account.email, membership.role, membership.status,
	       membership.version, membership.created_at, membership.updated_at
	FROM memberships AS membership
	JOIN users AS user_account ON user_account.id = membership.user_id`

type rowScanner interface {
	Scan(...any) error
}

func scanMember(row rowScanner) (Member, error) {
	var member Member
	err := row.Scan(
		&member.ID, &member.WorkspaceID, &member.Email, &member.Role,
		&member.Status, &member.Version, &member.CreatedAt, &member.UpdatedAt,
	)
	return member, err
}
