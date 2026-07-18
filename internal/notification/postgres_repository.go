package notification

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

func (repository *PostgresRepository) List(
	ctx context.Context,
	params ListParams,
) (RepositoryPage, error) {
	if repository == nil || repository.pool == nil {
		return RepositoryPage{}, fmt.Errorf("notification repository is not configured")
	}
	var highWatermark int64
	if err := repository.pool.QueryRow(ctx, `
		SELECT COALESCE(max(sequence), 0)
		FROM notifications
		WHERE workspace_id = $1 AND recipient_user_id = $2`,
		params.WorkspaceID, params.RecipientUserID,
	).Scan(&highWatermark); err != nil {
		return RepositoryPage{}, fmt.Errorf("query notification watermark: %w", err)
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT sequence, id::text, type, job_id::text, job_kind, state,
		       asset_id::text, result_revision_id::text, error_code, occurred_at
		FROM notifications
		WHERE workspace_id = $1
		  AND recipient_user_id = $2
		  AND sequence > $3
		  AND sequence <= $4
		ORDER BY sequence
		LIMIT $5`, params.WorkspaceID, params.RecipientUserID,
		params.AfterSequence, highWatermark, params.Limit)
	if err != nil {
		return RepositoryPage{}, fmt.Errorf("query notifications: %w", err)
	}
	defer rows.Close()
	items := make([]Event, 0)
	for rows.Next() {
		var item Event
		if err := rows.Scan(
			&item.Sequence, &item.ID, &item.Type, &item.JobID, &item.JobKind,
			&item.State, &item.AssetID, &item.ResultRevisionID, &item.ErrorCode,
			&item.OccurredAt,
		); err != nil {
			return RepositoryPage{}, fmt.Errorf("scan notification: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return RepositoryPage{}, fmt.Errorf("iterate notifications: %w", err)
	}
	return RepositoryPage{Items: items, HighWatermark: highWatermark}, nil
}

var _ Repository = (*PostgresRepository)(nil)
