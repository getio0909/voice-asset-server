package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sessionColumns = `
	id::text, workspace_id::text, created_by::text, client_session_id::text,
	COALESCE(provider_profile_id::text, ''), COALESCE(hotword_set_id::text, ''),
	idempotency_key, protocol_version, audio_encoding, sample_rate_hz, channels,
	frame_duration_ms, language, state, next_sequence, received_bytes,
	COALESCE(last_captured_at_ms, 0), COALESCE(final_transcript, ''),
	COALESCE(final_language, ''), COALESCE(final_provider_id, ''),
	COALESCE(client_archive_sha256, ''), COALESCE(captured_duration_ms, 0),
	COALESCE(last_error_code, ''), version, started_at, last_frame_at,
	interrupted_at, reconnect_by, expires_at, completed_at, updated_at`

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Create(
	ctx context.Context,
	session Session,
	requestHash string,
	audit Audit,
) (Session, bool, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Session{}, false, fmt.Errorf("begin realtime session transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if existing, existingHash, err := loadIdempotentSession(
		ctx, tx, session.WorkspaceID, session.IdempotencyKey,
	); err == nil {
		if existingHash != requestHash {
			return Session{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, fmt.Errorf("load idempotent realtime session: %w", err)
	}
	if err := validateSessionReferences(ctx, tx, session); err != nil {
		return Session{}, false, err
	}
	created, err := scanSession(tx.QueryRow(ctx, `
		INSERT INTO recording_sessions (
			id, workspace_id, created_by, client_session_id, provider_profile_id,
			hotword_set_id, idempotency_key, idempotency_request_hash,
			protocol_version, audio_encoding, sample_rate_hz, channels,
			frame_duration_ms, language, state, next_sequence, received_bytes,
			started_at, expires_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19, $18
		)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
		RETURNING `+sessionColumns,
		session.ID, session.WorkspaceID, session.CreatedBy, session.ClientSessionID,
		nullableUUID(session.ProviderProfileID), nullableUUID(session.HotwordSetID),
		session.IdempotencyKey, requestHash, ProtocolVersion, session.Encoding,
		session.SampleRateHz, session.Channels, session.FrameDurationMS,
		session.Language, session.State, session.NextSequence, session.ReceivedBytes,
		session.StartedAt, session.ExpiresAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, existingHash, loadErr := loadIdempotentSession(
			ctx, tx, session.WorkspaceID, session.IdempotencyKey,
		)
		if loadErr != nil {
			return Session{}, false, fmt.Errorf("load concurrently created realtime session: %w", loadErr)
		}
		if existingHash != requestHash {
			return Session{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	if isUniqueViolation(err) {
		return Session{}, false, ErrIdempotencyConflict
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("insert realtime session: %w", err)
	}
	if err := insertSessionAudit(ctx, tx, created, audit); err != nil {
		return Session{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, false, fmt.Errorf("commit realtime session: %w", err)
	}
	return created, false, nil
}

func (repository *PostgresRepository) Get(
	ctx context.Context,
	workspaceID,
	sessionID string,
) (Session, error) {
	result, err := scanSession(repository.pool.QueryRow(ctx, `
		SELECT `+sessionColumns+`
		FROM recording_sessions
		WHERE id = $1 AND workspace_id = $2`, sessionID, workspaceID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("query realtime session: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) Update(
	ctx context.Context,
	expectedVersion int64,
	session Session,
	audit *Audit,
) (Session, error) {
	if session.Version != expectedVersion+1 {
		return Session{}, ErrVersionConflict
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin realtime update: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	updated, err := scanSession(tx.QueryRow(ctx, `
		UPDATE recording_sessions
		SET state = $4,
		    next_sequence = $5,
		    received_bytes = $6,
		    last_captured_at_ms = $7,
		    final_transcript = $8,
		    final_language = $9,
		    final_provider_id = $10,
		    client_archive_sha256 = $11,
		    captured_duration_ms = $12,
		    last_error_code = $13,
		    version = $14,
		    last_frame_at = $15,
		    interrupted_at = $16,
		    reconnect_by = $17,
		    completed_at = $18,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND version = $3
		RETURNING `+sessionColumns,
		session.ID, session.WorkspaceID, expectedVersion, session.State,
		session.NextSequence, session.ReceivedBytes,
		nullableLastCapture(session), nullableText(session.FinalTranscript),
		nullableText(session.FinalLanguage), nullableText(session.FinalProviderID),
		nullableText(session.ClientArchiveSHA256), nullableDuration(session),
		nullableText(session.LastErrorCode), session.Version, session.LastFrameAt,
		session.InterruptedAt, session.ReconnectBy, session.CompletedAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrVersionConflict
	}
	if err != nil {
		return Session{}, fmt.Errorf("update realtime session: %w", err)
	}
	if audit != nil {
		if err := insertSessionAudit(ctx, tx, updated, *audit); err != nil {
			return Session{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit realtime update: %w", err)
	}
	return updated, nil
}

type sessionRow interface {
	Scan(destinations ...any) error
}

func scanSession(row sessionRow) (Session, error) {
	var result Session
	err := row.Scan(
		&result.ID, &result.WorkspaceID, &result.CreatedBy, &result.ClientSessionID,
		&result.ProviderProfileID, &result.HotwordSetID, &result.IdempotencyKey,
		new(string), &result.Encoding, &result.SampleRateHz, &result.Channels,
		&result.FrameDurationMS, &result.Language, &result.State,
		&result.NextSequence, &result.ReceivedBytes, &result.LastCapturedAtMS,
		&result.FinalTranscript, &result.FinalLanguage, &result.FinalProviderID,
		&result.ClientArchiveSHA256,
		&result.CapturedDurationMS, &result.LastErrorCode, &result.Version,
		&result.StartedAt, &result.LastFrameAt, &result.InterruptedAt,
		&result.ReconnectBy, &result.ExpiresAt, &result.CompletedAt, &result.UpdatedAt,
	)
	return result, err
}

func loadIdempotentSession(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID,
	idempotencyKey string,
) (Session, string, error) {
	var result Session
	var protocolVersion, requestHash string
	err := tx.QueryRow(ctx, `
		SELECT `+sessionColumns+`, idempotency_request_hash
		FROM recording_sessions
		WHERE workspace_id = $1 AND idempotency_key = $2`, workspaceID, idempotencyKey,
	).Scan(
		&result.ID, &result.WorkspaceID, &result.CreatedBy, &result.ClientSessionID,
		&result.ProviderProfileID, &result.HotwordSetID, &result.IdempotencyKey,
		&protocolVersion, &result.Encoding, &result.SampleRateHz, &result.Channels,
		&result.FrameDurationMS, &result.Language, &result.State,
		&result.NextSequence, &result.ReceivedBytes, &result.LastCapturedAtMS,
		&result.FinalTranscript, &result.FinalLanguage, &result.FinalProviderID,
		&result.ClientArchiveSHA256,
		&result.CapturedDurationMS, &result.LastErrorCode, &result.Version,
		&result.StartedAt, &result.LastFrameAt, &result.InterruptedAt,
		&result.ReconnectBy, &result.ExpiresAt, &result.CompletedAt, &result.UpdatedAt,
		&requestHash,
	)
	return result, requestHash, err
}

func validateSessionReferences(ctx context.Context, tx pgx.Tx, session Session) error {
	if session.ProviderProfileID != "" {
		var valid bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM provider_profiles
				WHERE id = $1 AND workspace_id = $2
				  AND provider_type = 'asr' AND state = 'enabled'
			)`, session.ProviderProfileID, session.WorkspaceID).Scan(&valid); err != nil {
			return fmt.Errorf("validate realtime provider profile: %w", err)
		}
		if !valid {
			return ErrInvalidEvent
		}
	}
	if session.HotwordSetID != "" {
		var valid bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM hotword_sets
				WHERE id = $1 AND workspace_id = $2 AND state = 'enabled'
			)`, session.HotwordSetID, session.WorkspaceID).Scan(&valid); err != nil {
			return fmt.Errorf("validate realtime hotword set: %w", err)
		}
		if !valid {
			return ErrInvalidEvent
		}
	}
	return nil
}

func insertSessionAudit(ctx context.Context, tx pgx.Tx, session Session, audit Audit) error {
	if !validSessionAudit(audit) {
		return ErrInvalidEvent
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, metadata
		) VALUES ($1, $2, $3, $4, $5, 'recording_session', $6, $7)`,
		audit.ID, session.WorkspaceID, nullableUUID(audit.ActorID), audit.ActorType,
		audit.Action, session.ID, audit.Metadata,
	)
	if err != nil {
		return fmt.Errorf("insert realtime session audit: %w", err)
	}
	return nil
}

func validSessionAudit(audit Audit) bool {
	if !canonicalUUID(audit.ID) || audit.ActorType != "user" || !canonicalUUID(audit.ActorID) ||
		!json.Valid(audit.Metadata) || len(audit.Metadata) > 4096 ||
		len(bytes.TrimSpace(audit.Metadata)) < 2 || bytes.TrimSpace(audit.Metadata)[0] != '{' {
		return false
	}
	switch audit.Action {
	case AuditSessionStarted, AuditSessionInterrupted, AuditSessionResumed,
		AuditSessionFinalizing, AuditSessionCompleted, AuditSessionFailed:
		return true
	default:
		return false
	}
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableLastCapture(session Session) any {
	if session.NextSequence == 0 {
		return nil
	}
	return session.LastCapturedAtMS
}

func nullableDuration(session Session) any {
	if session.ClientArchiveSHA256 == "" {
		return nil
	}
	return session.CapturedDurationMS
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
