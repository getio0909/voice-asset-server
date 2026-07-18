package assetpurge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Load(
	ctx context.Context,
	jobID,
	workerID string,
	now time.Time,
) (Inventory, error) {
	if repository == nil || repository.pool == nil || jobID == "" || workerID == "" || now.IsZero() {
		return Inventory{}, errors.New("invalid asset purge inventory request")
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return Inventory{}, fmt.Errorf("begin asset purge inventory: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var inventory Inventory
	var state, kind, leaseOwner, assetStatus string
	var leaseExpiresAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT job.id::text, job.workspace_id::text, job.asset_id::text,
		       job.state, job.kind, job.lease_owner, job.lease_expires_at,
		       asset.status
		FROM jobs AS job
		JOIN assets AS asset
		  ON asset.id = job.asset_id AND asset.workspace_id = job.workspace_id
		WHERE job.id = $1`, jobID,
	).Scan(
		&inventory.JobID, &inventory.WorkspaceID, &inventory.AssetID,
		&state, &kind, &leaseOwner, &leaseExpiresAt, &assetStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Inventory{}, job.ErrNotFound
	}
	if err != nil {
		return Inventory{}, fmt.Errorf("load asset purge lease: %w", err)
	}
	if state != job.StateRunning || kind != job.KindPurgeAsset || leaseOwner != workerID ||
		!leaseExpiresAt.After(now.UTC()) || assetStatus != "purging" {
		return Inventory{}, job.ErrLeaseConflict
	}

	rows, err := tx.Query(ctx, `
		SELECT id::text, storage_backend, storage_key, file_size, sha256
		FROM asset_objects
		WHERE asset_id = $1
		ORDER BY id
		LIMIT $2`, inventory.AssetID, MaxObjects+1)
	if err != nil {
		return Inventory{}, fmt.Errorf("query asset purge objects: %w", err)
	}
	for rows.Next() {
		var object Object
		if err := rows.Scan(&object.ID, &object.Backend, &object.Key, &object.Size, &object.SHA256); err != nil {
			rows.Close()
			return Inventory{}, fmt.Errorf("scan asset purge object: %w", err)
		}
		inventory.Objects = append(inventory.Objects, object)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Inventory{}, fmt.Errorf("iterate asset purge objects: %w", err)
	}
	rows.Close()
	if len(inventory.Objects) > MaxObjects {
		return Inventory{}, errors.New("asset purge object inventory exceeds limit")
	}

	uploadRows, err := tx.Query(ctx, `
		SELECT id::text
		FROM upload_sessions
		WHERE asset_id = $1
		ORDER BY id
		LIMIT $2`, inventory.AssetID, MaxUploads+1)
	if err != nil {
		return Inventory{}, fmt.Errorf("query asset purge uploads: %w", err)
	}
	for uploadRows.Next() {
		var uploadID string
		if err := uploadRows.Scan(&uploadID); err != nil {
			uploadRows.Close()
			return Inventory{}, fmt.Errorf("scan asset purge upload: %w", err)
		}
		inventory.UploadIDs = append(inventory.UploadIDs, uploadID)
	}
	if err := uploadRows.Err(); err != nil {
		uploadRows.Close()
		return Inventory{}, fmt.Errorf("iterate asset purge uploads: %w", err)
	}
	uploadRows.Close()
	if len(inventory.UploadIDs) > MaxUploads {
		return Inventory{}, errors.New("asset purge upload inventory exceeds limit")
	}
	inventory.Fingerprint = inventoryFingerprint(inventory.Objects, inventory.UploadIDs)
	if err := tx.Commit(ctx); err != nil {
		return Inventory{}, fmt.Errorf("commit asset purge inventory: %w", err)
	}
	return inventory, nil
}

func (repository *PostgresRepository) Finalize(
	ctx context.Context,
	inventory Inventory,
	workerID,
	auditID string,
	now time.Time,
) error {
	if repository == nil || repository.pool == nil || workerID == "" || auditID == "" || now.IsZero() {
		return errors.New("invalid asset purge finalization")
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin asset purge finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, `SELECT GREATEST($1::timestamptz, clock_timestamp())`, now.UTC()).Scan(&now); err != nil {
		return fmt.Errorf("resolve asset purge clock: %w", err)
	}

	var state, kind, workspaceID, assetID, createdBy string
	var leaseOwner *string
	var leaseExpiresAt *time.Time
	var attempt int
	err = tx.QueryRow(ctx, `
		SELECT state, kind, workspace_id::text, asset_id::text, created_by::text,
		       lease_owner, lease_expires_at, attempts
		FROM jobs
		WHERE id = $1
		FOR UPDATE`, inventory.JobID,
	).Scan(&state, &kind, &workspaceID, &assetID, &createdBy, &leaseOwner, &leaseExpiresAt, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return job.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock asset purge job: %w", err)
	}
	if state != job.StateRunning || kind != job.KindPurgeAsset ||
		workspaceID != inventory.WorkspaceID || assetID != inventory.AssetID ||
		leaseOwner == nil || *leaseOwner != workerID || leaseExpiresAt == nil ||
		!leaseExpiresAt.After(now) {
		return job.ErrLeaseConflict
	}
	var assetStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM assets
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, inventory.AssetID, inventory.WorkspaceID,
	).Scan(&assetStatus); errors.Is(err, pgx.ErrNoRows) {
		return job.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock purging asset: %w", err)
	}
	if assetStatus != "purging" {
		return job.ErrLeaseConflict
	}

	current, err := loadInventoryInTransaction(ctx, tx, inventory)
	if err != nil {
		return err
	}
	if current.Fingerprint != inventory.Fingerprint {
		return ErrInventoryChanged
	}
	if err := deleteAssetGraph(ctx, tx, inventory.AssetID, inventory.JobID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		UPDATE job_attempts
		SET outcome = 'succeeded', error_code = NULL, finished_at = $3
		WHERE job_id = $1 AND attempt = $2 AND outcome IS NULL`,
		inventory.JobID, attempt, now,
	)
	if err != nil {
		return fmt.Errorf("finish asset purge attempt: %w", err)
	}
	if command.RowsAffected() != 1 {
		return job.ErrLeaseConflict
	}
	command, err = tx.Exec(ctx, `
		UPDATE jobs
		SET state = 'succeeded', asset_id = NULL, lease_owner = NULL,
		    lease_expires_at = NULL, last_error_code = NULL, updated_at = $2
		WHERE id = $1 AND state = 'running'`, inventory.JobID, now)
	if err != nil {
		return fmt.Errorf("succeed asset purge job: %w", err)
	}
	if command.RowsAffected() != 1 {
		return job.ErrLeaseConflict
	}
	command, err = tx.Exec(ctx, `
		DELETE FROM assets
		WHERE id = $1 AND workspace_id = $2 AND status = 'purging'`,
		inventory.AssetID, inventory.WorkspaceID,
	)
	if err != nil {
		return fmt.Errorf("delete purged asset: %w", err)
	}
	if command.RowsAffected() != 1 {
		return job.ErrLeaseConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, metadata, occurred_at
		) VALUES (
			$1, $2, NULL, 'system', 'asset.purged', 'asset', $3,
			jsonb_build_object(
				'job_id', $4::uuid::text, 'requested_by', $5::uuid::text,
				'object_count', $6::integer, 'upload_count', $7::integer,
				'inventory_sha256', $8::text
			), $9
		)`, auditID, inventory.WorkspaceID, inventory.AssetID, inventory.JobID,
		createdBy, len(inventory.Objects), len(inventory.UploadIDs),
		inventory.Fingerprint, now,
	); err != nil {
		return fmt.Errorf("audit permanent asset deletion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit permanent asset deletion: %w", err)
	}
	return nil
}

func loadInventoryInTransaction(ctx context.Context, tx pgx.Tx, expected Inventory) (Inventory, error) {
	current := Inventory{JobID: expected.JobID, WorkspaceID: expected.WorkspaceID, AssetID: expected.AssetID}
	rows, err := tx.Query(ctx, `
		SELECT id::text, storage_backend, storage_key, file_size, sha256
		FROM asset_objects WHERE asset_id = $1 ORDER BY id LIMIT $2`,
		expected.AssetID, MaxObjects+1,
	)
	if err != nil {
		return Inventory{}, fmt.Errorf("requery asset purge objects: %w", err)
	}
	for rows.Next() {
		var object Object
		if err := rows.Scan(&object.ID, &object.Backend, &object.Key, &object.Size, &object.SHA256); err != nil {
			rows.Close()
			return Inventory{}, fmt.Errorf("rescan asset purge object: %w", err)
		}
		current.Objects = append(current.Objects, object)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Inventory{}, fmt.Errorf("reiterate asset purge objects: %w", err)
	}
	rows.Close()
	if len(current.Objects) > MaxObjects {
		return Inventory{}, ErrInventoryChanged
	}
	uploadRows, err := tx.Query(ctx, `
		SELECT id::text FROM upload_sessions
		WHERE asset_id = $1 ORDER BY id LIMIT $2`, expected.AssetID, MaxUploads+1)
	if err != nil {
		return Inventory{}, fmt.Errorf("requery asset purge uploads: %w", err)
	}
	for uploadRows.Next() {
		var uploadID string
		if err := uploadRows.Scan(&uploadID); err != nil {
			uploadRows.Close()
			return Inventory{}, fmt.Errorf("rescan asset purge upload: %w", err)
		}
		current.UploadIDs = append(current.UploadIDs, uploadID)
	}
	if err := uploadRows.Err(); err != nil {
		uploadRows.Close()
		return Inventory{}, fmt.Errorf("reiterate asset purge uploads: %w", err)
	}
	uploadRows.Close()
	if len(current.UploadIDs) > MaxUploads {
		return Inventory{}, ErrInventoryChanged
	}
	current.Fingerprint = inventoryFingerprint(current.Objects, current.UploadIDs)
	return current, nil
}

func deleteAssetGraph(ctx context.Context, tx pgx.Tx, assetID, purgeJobID string) error {
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{"clip metadata", `DELETE FROM audio_clips WHERE asset_id = $1`, []any{assetID}},
		{"export metadata", `DELETE FROM transcript_exports WHERE asset_id = $1`, []any{assetID}},
		{"job revision links", `UPDATE jobs SET result_revision_id = NULL WHERE asset_id = $1`, []any{assetID}},
		{"transcript reviews", `DELETE FROM transcript_revision_reviews WHERE revision_id IN (
			SELECT revision.id FROM transcript_revisions revision
			JOIN transcripts transcript ON transcript.id = revision.transcript_id
			WHERE transcript.asset_id = $1
		)`, []any{assetID}},
		{"transcript segments", `DELETE FROM transcript_segments WHERE revision_id IN (
			SELECT revision.id FROM transcript_revisions revision
			JOIN transcripts transcript ON transcript.id = revision.transcript_id
			WHERE transcript.asset_id = $1
		)`, []any{assetID}},
		{"transcript revisions", `DELETE FROM transcript_revisions WHERE transcript_id IN (
			SELECT id FROM transcripts WHERE asset_id = $1
		)`, []any{assetID}},
		{"transcripts", `DELETE FROM transcripts WHERE asset_id = $1`, []any{assetID}},
		{"asset objects", `DELETE FROM asset_objects WHERE asset_id = $1`, []any{assetID}},
		{"upload sessions", `DELETE FROM upload_sessions WHERE asset_id = $1`, []any{assetID}},
		{"hotword versions", `DELETE FROM hotword_set_versions WHERE hotword_set_id IN (
			SELECT id FROM hotword_sets WHERE scope_type = 'asset' AND scope_id = $1
		)`, []any{assetID}},
		{"hotword sets", `DELETE FROM hotword_sets WHERE scope_type = 'asset' AND scope_id = $1`, []any{assetID}},
		{"glossary versions", `DELETE FROM glossary_set_versions WHERE glossary_set_id IN (
			SELECT id FROM glossary_sets WHERE scope_type = 'asset' AND scope_id = $1
		)`, []any{assetID}},
		{"glossary sets", `DELETE FROM glossary_sets WHERE scope_type = 'asset' AND scope_id = $1`, []any{assetID}},
		{"prior jobs", `DELETE FROM jobs WHERE asset_id = $1 AND id <> $2`, []any{assetID, purgeJobID}},
	}
	for _, operation := range queries {
		if _, err := tx.Exec(ctx, operation.query, operation.args...); err != nil {
			return fmt.Errorf("delete asset %s: %w", operation.name, err)
		}
	}
	return nil
}

var _ Repository = (*PostgresRepository)(nil)
