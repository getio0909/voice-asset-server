package audit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Record(ctx context.Context, entry Entry) error {
	var targetID any
	if entry.TargetID != "" {
		targetID = entry.TargetID
	}
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7::uuid, $8, $9::jsonb)`,
		entry.ID, entry.WorkspaceID, entry.ActorID, entry.ActorType, entry.Action,
		entry.TargetType, targetID, entry.RequestID, entry.Metadata,
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}
