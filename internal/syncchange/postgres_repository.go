package syncchange

import (
	"context"
	"encoding/json"
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
		return RepositoryPage{}, fmt.Errorf("sync change repository is not configured")
	}
	var highWatermark int64
	if err := repository.pool.QueryRow(ctx, `
		SELECT COALESCE(max(sequence), 0)
		FROM sync_changes
		WHERE workspace_id = $1`, params.WorkspaceID).Scan(&highWatermark); err != nil {
		return RepositoryPage{}, fmt.Errorf("query sync change watermark: %w", err)
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT sequence, entity_type, entity_id::text, operation,
		       entity_version, changed_at, payload
		FROM sync_changes
		WHERE workspace_id = $1
		  AND sequence > $2
		  AND sequence <= $3
		ORDER BY sequence
		LIMIT $4`, params.WorkspaceID, params.AfterSequence, highWatermark, params.Limit)
	if err != nil {
		return RepositoryPage{}, fmt.Errorf("query sync changes: %w", err)
	}
	defer rows.Close()
	items := make([]Change, 0)
	for rows.Next() {
		var item Change
		var payload []byte
		if err := rows.Scan(
			&item.Sequence, &item.EntityType, &item.EntityID, &item.Operation,
			&item.EntityVersion, &item.ChangedAt, &payload,
		); err != nil {
			return RepositoryPage{}, fmt.Errorf("scan sync change: %w", err)
		}
		if item.Operation == "upsert" {
			if len(payload) == 0 {
				return RepositoryPage{}, fmt.Errorf("sync upsert payload is missing")
			}
			var snapshot AssetSnapshot
			if err := json.Unmarshal(payload, &snapshot); err != nil {
				return RepositoryPage{}, fmt.Errorf("decode sync asset snapshot: %w", err)
			}
			item.Asset = &snapshot
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return RepositoryPage{}, fmt.Errorf("iterate sync changes: %w", err)
	}
	return RepositoryPage{Items: items, HighWatermark: highWatermark}, nil
}

var _ Repository = (*PostgresRepository)(nil)
