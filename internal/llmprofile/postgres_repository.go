package llmprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Create(ctx context.Context, params CreateParams) (Profile, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin LLM profile transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var result Profile
	var configJSON []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO provider_profiles (
			id, workspace_id, provider_type, provider_id, display_name, config,
			secret_ciphertext, state, priority, created_by
		) VALUES ($1, $2, 'llm', $3, $4, $5, $6, $7, $8, $9)
		RETURNING id::text, workspace_id::text, provider_id, display_name, config,
		          state, priority, version, secret_ciphertext IS NOT NULL,
		          created_at, updated_at`,
		params.ProfileID, params.WorkspaceID, params.ProviderID, params.DisplayName,
		params.ConfigJSON, nullableCiphertext(params.SecretCiphertext), params.State,
		params.Priority, params.CreatedBy,
	).Scan(&result.ID, &result.WorkspaceID, &result.ProviderID, &result.DisplayName,
		&configJSON, &result.State, &result.Priority, &result.Version,
		&result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		if uniqueViolation(err) {
			return Profile{}, ErrConflict
		}
		return Profile{}, fmt.Errorf("insert LLM profile: %w", err)
	}
	result.Config, err = decodeConfig(configJSON)
	if err != nil {
		return Profile{}, fmt.Errorf("decode inserted LLM config: %w", err)
	}
	metadata, err := json.Marshal(struct {
		ProviderID string `json:"provider_id"`
		State      string `json:"state"`
		Priority   int    `json:"priority"`
	}{params.ProviderID, params.State, params.Priority})
	if err != nil {
		return Profile{}, fmt.Errorf("encode LLM profile audit: %w", err)
	}
	if err := insertProfileAudit(ctx, tx, params.AuditID, params.WorkspaceID,
		params.CreatedBy, "llm_profile.created", params.ProfileID, metadata); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit LLM profile transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) List(ctx context.Context, workspaceID string) ([]Profile, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, provider_id, display_name, config,
		       state, priority, version, secret_ciphertext IS NOT NULL,
		       created_at, updated_at
		FROM provider_profiles
		WHERE workspace_id = $1 AND provider_type = 'llm'
		ORDER BY priority, lower(display_name), id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query LLM profiles: %w", err)
	}
	defer rows.Close()
	profiles := make([]Profile, 0)
	for rows.Next() {
		profile, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate LLM profiles: %w", err)
	}
	return profiles, nil
}

func (repository *PostgresRepository) ListEnabled(ctx context.Context, workspaceID string) ([]StoredProfile, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, provider_id, display_name, config,
		       state, priority, version, secret_ciphertext IS NOT NULL,
		       created_at, updated_at, secret_ciphertext
		FROM provider_profiles
		WHERE workspace_id = $1 AND provider_type = 'llm' AND state = 'enabled'
		ORDER BY priority, id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query enabled LLM profiles: %w", err)
	}
	defer rows.Close()
	profiles := make([]StoredProfile, 0)
	for rows.Next() {
		profile, err := scanStoredProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled LLM profiles: %w", err)
	}
	return profiles, nil
}

func (repository *PostgresRepository) GetStored(ctx context.Context, workspaceID, profileID string) (StoredProfile, error) {
	result, err := scanStoredProfile(repository.pool.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, provider_id, display_name, config,
		       state, priority, version, secret_ciphertext IS NOT NULL,
		       created_at, updated_at, secret_ciphertext
		FROM provider_profiles
		WHERE id = $1 AND workspace_id = $2 AND provider_type = 'llm'`, profileID, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredProfile{}, ErrNotFound
	}
	return result, err
}

func (repository *PostgresRepository) Update(ctx context.Context, params UpdateParams) (Profile, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin LLM profile update: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var result Profile
	var configJSON []byte
	err = tx.QueryRow(ctx, `
		UPDATE provider_profiles
		SET display_name = $3, config = $4, secret_ciphertext = $5,
		    state = $6, priority = $7, version = version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND provider_type = 'llm' AND version = $8
		RETURNING id::text, workspace_id::text, provider_id, display_name, config,
		          state, priority, version, secret_ciphertext IS NOT NULL,
		          created_at, updated_at`,
		params.ProfileID, params.WorkspaceID, params.DisplayName, params.ConfigJSON,
		nullableCiphertext(params.SecretCiphertext), params.State, params.Priority, params.ExpectedVersion,
	).Scan(&result.ID, &result.WorkspaceID, &result.ProviderID, &result.DisplayName,
		&configJSON, &result.State, &result.Priority, &result.Version,
		&result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || uniqueViolation(err) {
		return Profile{}, ErrConflict
	}
	if err != nil {
		return Profile{}, fmt.Errorf("update LLM profile: %w", err)
	}
	result.Config, err = decodeConfig(configJSON)
	if err != nil {
		return Profile{}, fmt.Errorf("decode updated LLM config: %w", err)
	}
	metadata, err := json.Marshal(struct {
		State    string `json:"state"`
		Priority int    `json:"priority"`
		Version  int64  `json:"version"`
	}{params.State, params.Priority, result.Version})
	if err != nil {
		return Profile{}, fmt.Errorf("encode LLM update audit: %w", err)
	}
	if err := insertProfileAudit(ctx, tx, params.AuditID, params.WorkspaceID,
		params.UpdatedBy, "llm_profile.updated", params.ProfileID, metadata); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit LLM profile update: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) RecordHealth(ctx context.Context, profileID, status string, class llm.ErrorClass) error {
	var errorClass any
	if class != "" {
		errorClass = string(class)
	}
	if _, err := repository.pool.Exec(ctx, `
		INSERT INTO provider_profile_health_checks (profile_id, status, error_class)
		VALUES ($1, $2, $3)`, profileID, status, errorClass); err != nil {
		return fmt.Errorf("insert LLM profile health: %w", err)
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanProfile(row rowScanner) (Profile, error) {
	var result Profile
	var configJSON []byte
	if err := row.Scan(&result.ID, &result.WorkspaceID, &result.ProviderID,
		&result.DisplayName, &configJSON, &result.State, &result.Priority,
		&result.Version, &result.SecretConfigured, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return Profile{}, fmt.Errorf("scan LLM profile: %w", err)
	}
	config, err := decodeConfig(configJSON)
	if err != nil {
		return Profile{}, fmt.Errorf("decode LLM profile config: %w", err)
	}
	result.Config = config
	return result, nil
}

func scanStoredProfile(row rowScanner) (StoredProfile, error) {
	var result StoredProfile
	var configJSON []byte
	if err := row.Scan(&result.ID, &result.WorkspaceID, &result.ProviderID,
		&result.DisplayName, &configJSON, &result.State, &result.Priority,
		&result.Version, &result.SecretConfigured, &result.CreatedAt,
		&result.UpdatedAt, &result.SecretCiphertext); err != nil {
		return StoredProfile{}, err
	}
	config, err := decodeConfig(configJSON)
	if err != nil {
		return StoredProfile{}, fmt.Errorf("decode stored LLM config: %w", err)
	}
	result.Config = config
	result.SecretCiphertext = append([]byte(nil), result.SecretCiphertext...)
	return result, nil
}

func insertProfileAudit(ctx context.Context, tx pgx.Tx, auditID, workspaceID, actorID, action, profileID string, metadata []byte) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', $4, 'provider_profile', $5, $6)`,
		auditID, workspaceID, actorID, action, profileID, metadata); err != nil {
		return fmt.Errorf("insert LLM profile audit: %w", err)
	}
	return nil
}

func nullableCiphertext(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func uniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
