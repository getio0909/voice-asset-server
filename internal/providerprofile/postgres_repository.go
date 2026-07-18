package providerprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/getio0909/voice-asset-server/internal/asr"
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

func (repository *PostgresRepository) Create(
	ctx context.Context,
	params CreateParams,
) (Profile, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin provider profile transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var result Profile
	var configJSON []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO provider_profiles (
			id, workspace_id, provider_type, provider_id, display_name, config,
			secret_ciphertext, state, priority, created_by
		) VALUES ($1, $2, 'asr', $3, $4, $5, $6, $7, $8, $9)
		RETURNING id::text, workspace_id::text, provider_id, display_name, config,
		          state, priority, version, secret_ciphertext IS NOT NULL,
		          created_at, updated_at`,
		params.ProfileID, params.WorkspaceID, params.ProviderID, params.DisplayName,
		params.ConfigJSON, nullableCiphertext(params.SecretCiphertext), params.State,
		params.Priority, params.CreatedBy,
	).Scan(
		&result.ID, &result.WorkspaceID, &result.ProviderID, &result.DisplayName,
		&configJSON, &result.State, &result.Priority, &result.Version,
		&result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Profile{}, ErrConflict
		}
		return Profile{}, fmt.Errorf("insert provider profile: %w", err)
	}
	result.Config, err = decodeConfig(configJSON)
	if err != nil {
		return Profile{}, fmt.Errorf("decode inserted provider profile config: %w", err)
	}
	metadata, err := json.Marshal(struct {
		ProviderID  string `json:"provider_id"`
		DisplayName string `json:"display_name"`
		State       string `json:"state"`
		Priority    int    `json:"priority"`
	}{params.ProviderID, params.DisplayName, params.State, params.Priority})
	if err != nil {
		return Profile{}, fmt.Errorf("encode provider profile audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'provider_profile.created', 'provider_profile', $4, $5)`,
		params.AuditID, params.WorkspaceID, params.CreatedBy, params.ProfileID, metadata,
	); err != nil {
		return Profile{}, fmt.Errorf("insert provider profile audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit provider profile transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) List(
	ctx context.Context,
	workspaceID string,
) ([]Profile, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, provider_id, display_name, config,
		       state, priority, version, secret_ciphertext IS NOT NULL,
		       created_at, updated_at
		FROM provider_profiles
		WHERE workspace_id = $1 AND provider_type = 'asr'
		ORDER BY priority, lower(display_name), id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query provider profiles: %w", err)
	}
	defer rows.Close()
	profiles := make([]Profile, 0)
	for rows.Next() {
		profile, scanErr := scanProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider profiles: %w", err)
	}
	return profiles, nil
}

func (repository *PostgresRepository) ListEnabledASR(
	ctx context.Context,
	workspaceID string,
) ([]StoredProfile, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, provider_id, display_name, config,
		       state, priority, version, secret_ciphertext IS NOT NULL,
		       created_at, updated_at, secret_ciphertext
		FROM provider_profiles
		WHERE workspace_id = $1 AND provider_type = 'asr' AND state = 'enabled'
		ORDER BY priority, id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query enabled ASR profiles: %w", err)
	}
	defer rows.Close()
	profiles := make([]StoredProfile, 0)
	for rows.Next() {
		var result StoredProfile
		var configJSON []byte
		if err := rows.Scan(
			&result.ID, &result.WorkspaceID, &result.ProviderID, &result.DisplayName,
			&configJSON, &result.State, &result.Priority, &result.Version,
			&result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt,
			&result.SecretCiphertext,
		); err != nil {
			return nil, fmt.Errorf("scan enabled ASR profile: %w", err)
		}
		result.Config, err = decodeConfig(configJSON)
		if err != nil {
			return nil, fmt.Errorf("decode enabled ASR profile config: %w", err)
		}
		result.SecretCiphertext = append([]byte(nil), result.SecretCiphertext...)
		profiles = append(profiles, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled ASR profiles: %w", err)
	}
	return profiles, nil
}

func (repository *PostgresRepository) GetStored(
	ctx context.Context,
	workspaceID,
	profileID string,
) (StoredProfile, error) {
	var result StoredProfile
	var configJSON []byte
	err := repository.pool.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, provider_id, display_name, config,
		       state, priority, version, secret_ciphertext IS NOT NULL,
		       created_at, updated_at, secret_ciphertext
		FROM provider_profiles
		WHERE id = $1 AND workspace_id = $2 AND provider_type = 'asr'`,
		profileID, workspaceID,
	).Scan(
		&result.ID, &result.WorkspaceID, &result.ProviderID, &result.DisplayName,
		&configJSON, &result.State, &result.Priority, &result.Version,
		&result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt,
		&result.SecretCiphertext,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredProfile{}, ErrNotFound
	}
	if err != nil {
		return StoredProfile{}, fmt.Errorf("query provider profile: %w", err)
	}
	result.Config, err = decodeConfig(configJSON)
	if err != nil {
		return StoredProfile{}, fmt.Errorf("decode provider profile config: %w", err)
	}
	result.SecretCiphertext = append([]byte(nil), result.SecretCiphertext...)
	return result, nil
}

func (repository *PostgresRepository) Update(
	ctx context.Context,
	params UpdateParams,
) (Profile, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin provider profile update: %w", err)
	}
	defer tx.Rollback(ctx)
	var result Profile
	var configJSON []byte
	err = tx.QueryRow(ctx, `
		UPDATE provider_profiles
		SET display_name = $3, config = $4, secret_ciphertext = $5,
		    state = $6, priority = $7, version = version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND provider_type = 'asr' AND version = $8
		RETURNING id::text, workspace_id::text, provider_id, display_name, config,
		          state, priority, version, secret_ciphertext IS NOT NULL,
		          created_at, updated_at`,
		params.ProfileID, params.WorkspaceID, params.DisplayName, params.ConfigJSON,
		nullableCiphertext(params.SecretCiphertext), params.State, params.Priority,
		params.ExpectedVersion,
	).Scan(
		&result.ID, &result.WorkspaceID, &result.ProviderID, &result.DisplayName,
		&configJSON, &result.State, &result.Priority, &result.Version,
		&result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) || isUniqueViolation(err) {
		return Profile{}, ErrConflict
	}
	if err != nil {
		return Profile{}, fmt.Errorf("update provider profile: %w", err)
	}
	result.Config, err = decodeConfig(configJSON)
	if err != nil {
		return Profile{}, fmt.Errorf("decode updated provider profile config: %w", err)
	}
	metadata, err := json.Marshal(struct {
		State    string `json:"state"`
		Priority int    `json:"priority"`
		Version  int64  `json:"version"`
	}{params.State, params.Priority, result.Version})
	if err != nil {
		return Profile{}, fmt.Errorf("encode provider profile update audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'provider_profile.updated', 'provider_profile', $4, $5)`,
		params.AuditID, params.WorkspaceID, params.UpdatedBy, params.ProfileID, metadata,
	); err != nil {
		return Profile{}, fmt.Errorf("insert provider profile update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit provider profile update: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) RecordHealth(
	ctx context.Context,
	profileID,
	status string,
	errorClass asr.ErrorClass,
) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO provider_profile_health_checks (profile_id, status, error_class)
		VALUES ($1, $2, $3)`, profileID, status, nullableErrorClass(errorClass))
	if err != nil {
		return fmt.Errorf("insert provider profile health: %w", err)
	}
	return nil
}

type profileRow interface {
	Scan(destinations ...any) error
}

func scanProfile(row profileRow) (Profile, error) {
	var result Profile
	var configJSON []byte
	if err := row.Scan(
		&result.ID, &result.WorkspaceID, &result.ProviderID, &result.DisplayName,
		&configJSON, &result.State, &result.Priority, &result.Version,
		&result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt,
	); err != nil {
		return Profile{}, fmt.Errorf("scan provider profile: %w", err)
	}
	config, err := decodeConfig(configJSON)
	if err != nil {
		return Profile{}, fmt.Errorf("decode provider profile config: %w", err)
	}
	result.Config = config
	return result, nil
}

func nullableCiphertext(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableErrorClass(value asr.ErrorClass) any {
	if value == "" {
		return nil
	}
	return string(value)
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
