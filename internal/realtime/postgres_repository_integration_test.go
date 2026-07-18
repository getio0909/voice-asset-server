package realtime_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/realtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	realtimeWorkspace      = "91000000-0000-4000-8000-000000000001"
	realtimeOtherWorkspace = "91000000-0000-4000-8000-000000000002"
	realtimeUser           = "92000000-0000-4000-8000-000000000001"
	realtimeOtherUser      = "92000000-0000-4000-8000-000000000002"
	realtimeOtherProvider  = "93000000-0000-4000-8000-000000000001"
)

func TestPostgresRealtimeLifecycleIsIdempotentScopedAndAudited(t *testing.T) {
	pool := migratedRealtimePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	seedRealtimePrincipals(t, ctx, pool)
	service := realtime.NewService(realtime.NewPostgresRepository(pool))
	principal := auth.Principal{
		WorkspaceID: realtimeWorkspace, UserID: realtimeUser,
		Scopes: []string{auth.ScopeTranscriptionsWrite},
	}

	start := realtimeStart("94000000-0000-4000-8000-000000000001", "realtime-db-1")
	created, replayed, err := service.Start(ctx, principal, start)
	if err != nil || replayed || created.State != realtime.StateStreaming || created.Version != 1 {
		t.Fatalf("Start() = (%+v, %t, %v)", created, replayed, err)
	}
	replayedSession, replayed, err := service.Start(ctx, principal, start)
	if err != nil || !replayed || replayedSession.ID != created.ID {
		t.Fatalf("Start(replay) = (%+v, %t, %v)", replayedSession, replayed, err)
	}
	conflicting := start
	conflicting.Language = "zh-CN"
	if _, _, err := service.Start(ctx, principal, conflicting); !errors.Is(err, realtime.ErrIdempotencyConflict) {
		t.Fatalf("Start(conflict) error = %v", err)
	}

	frame := realtime.AudioEvent{
		Type: realtime.EventAudio, SessionID: created.ID, Sequence: 0, CapturedAtMS: 0,
		PCMBase64: base64.StdEncoding.EncodeToString(make([]byte, 640)),
	}
	accepted, disposition, err := service.AcceptAudio(ctx, principal, frame)
	if err != nil || disposition != realtime.FrameAccepted || accepted.NextSequence != 1 || accepted.ReceivedBytes != 640 {
		t.Fatalf("AcceptAudio() = (%+v, %q, %v)", accepted, disposition, err)
	}
	duplicate, disposition, err := service.AcceptAudio(ctx, principal, frame)
	if err != nil || disposition != realtime.FrameDuplicate || duplicate.Version != accepted.Version {
		t.Fatalf("AcceptAudio(replay) = (%+v, %q, %v)", duplicate, disposition, err)
	}

	interrupted, err := service.Interrupt(ctx, principal, created.ID)
	if err != nil || interrupted.State != realtime.StateInterrupted {
		t.Fatalf("Interrupt() = (%+v, %v)", interrupted, err)
	}
	resumed, err := service.Resume(ctx, principal, realtime.ResumeEvent{
		Type: realtime.EventResume, ProtocolVersion: realtime.ProtocolVersion,
		SessionID: created.ID, LastAcknowledgedSequence: -1,
	})
	if err != nil || resumed.State != realtime.StateStreaming || resumed.NextSequence != 1 {
		t.Fatalf("Resume() = (%+v, %v)", resumed, err)
	}
	finalizing, err := service.BeginFinalization(ctx, principal, realtime.FinishEvent{
		Type: realtime.EventFinish, SessionID: created.ID, FinalSequence: 0,
		CapturedDurationMS: 20, ClientArchiveSHA256: strings.Repeat("a", 64),
	})
	if err != nil || finalizing.State != realtime.StateFinalizing {
		t.Fatalf("BeginFinalization() = (%+v, %v)", finalizing, err)
	}
	completed, err := service.Complete(ctx, principal, created.ID, realtime.ProviderResult{
		Text: "temporary realtime final", Language: "en-US", ProviderID: "mock_asr",
	})
	if err != nil || completed.State != realtime.StateCompleted || completed.CompletedAt == nil {
		t.Fatalf("Complete() = (%+v, %v)", completed, err)
	}

	otherPrincipal := auth.Principal{
		WorkspaceID: realtimeOtherWorkspace, UserID: realtimeOtherUser,
		Scopes: []string{auth.ScopeTranscriptionsWrite},
	}
	if _, err := service.Get(ctx, otherPrincipal, created.ID); !errors.Is(err, realtime.ErrNotFound) {
		t.Fatalf("Get(other workspace) error = %v", err)
	}
	crossWorkspaceProfile := realtimeStart("94000000-0000-4000-8000-000000000002", "realtime-db-2")
	crossWorkspaceProfile.ProviderProfileID = realtimeOtherProvider
	if _, _, err := service.Start(ctx, principal, crossWorkspaceProfile); !errors.Is(err, realtime.ErrInvalidEvent) {
		t.Fatalf("Start(cross-workspace profile) error = %v", err)
	}

	var state string
	var nextSequence, receivedBytes, version int64
	var transcript, finalLanguage, finalProviderID string
	if err := pool.QueryRow(ctx, `
		SELECT state, next_sequence, received_bytes, version, final_transcript,
		       final_language, final_provider_id
		FROM recording_sessions WHERE id = $1`, created.ID,
	).Scan(
		&state, &nextSequence, &receivedBytes, &version, &transcript,
		&finalLanguage, &finalProviderID,
	); err != nil {
		t.Fatalf("query persisted realtime session: %v", err)
	}
	if state != realtime.StateCompleted || nextSequence != 1 || receivedBytes != 640 ||
		version != 6 || transcript != "temporary realtime final" ||
		finalLanguage != "en-US" || finalProviderID != "mock_asr" {
		t.Fatalf(
			"persisted session = %q/%d/%d/v%d/%q/%q/%q",
			state, nextSequence, receivedBytes, version, transcript, finalLanguage, finalProviderID,
		)
	}
	rows, err := pool.Query(ctx, `
		SELECT action, metadata::text
		FROM audit_logs
		WHERE target_type = 'recording_session' AND target_id = $1
		ORDER BY occurred_at, id`, created.ID)
	if err != nil {
		t.Fatalf("query realtime audits: %v", err)
	}
	defer rows.Close()
	actions := make([]string, 0)
	for rows.Next() {
		var action, metadata string
		if err := rows.Scan(&action, &metadata); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(metadata, transcript) {
			t.Fatal("audit metadata contains transcript text")
		}
		actions = append(actions, action)
	}
	wantActions := []string{
		realtime.AuditSessionStarted, realtime.AuditSessionInterrupted,
		realtime.AuditSessionResumed, realtime.AuditSessionFinalizing,
		realtime.AuditSessionCompleted,
	}
	if strings.Join(actions, ",") != strings.Join(wantActions, ",") {
		t.Fatalf("audit actions = %v, want %v", actions, wantActions)
	}
}

func TestRecordingSessionDatabaseConstraintsRejectInconsistentProgress(t *testing.T) {
	pool := migratedRealtimePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	seedRealtimePrincipals(t, ctx, pool)
	service := realtime.NewService(realtime.NewPostgresRepository(pool))
	principal := auth.Principal{
		WorkspaceID: realtimeWorkspace, UserID: realtimeUser,
		Scopes: []string{auth.ScopeTranscriptionsWrite},
	}
	created, _, err := service.Start(ctx, principal, realtimeStart(
		"94000000-0000-4000-8000-000000000003", "realtime-db-constraint",
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE recording_sessions SET next_sequence = 1 WHERE id = $1`, created.ID,
	); err == nil {
		t.Fatal("database accepted a sequence without frame metadata")
	}
}

func realtimeStart(clientSessionID, idempotencyKey string) realtime.StartEvent {
	return realtime.StartEvent{
		Type: realtime.EventStart, ProtocolVersion: realtime.ProtocolVersion,
		ClientSessionID: clientSessionID, IdempotencyKey: idempotencyKey,
		Encoding: realtime.EncodingPCMS16LE, SampleRateHz: 16000, Channels: 1,
		FrameDurationMS: 20, Language: "en-US",
	}
}

func seedRealtimePrincipals(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name) VALUES ($1, 'Realtime'), ($2, 'Other');
		INSERT INTO users (id, email, password_hash, status) VALUES
			($3, 'realtime@example.test', 'hash', 'active'),
			($4, 'other-realtime@example.test', 'hash', 'active');
		INSERT INTO memberships (workspace_id, user_id, role) VALUES
			($1, $3, 'owner'), ($2, $4, 'owner');
		INSERT INTO provider_profiles (
			id, workspace_id, provider_type, provider_id, display_name, config,
			state, created_by
		) VALUES ($5, $2, 'asr', 'mock_asr', 'Other Mock', '{}', 'enabled', $4)`,
		realtimeWorkspace, realtimeOtherWorkspace, realtimeUser, realtimeOtherUser,
		realtimeOtherProvider,
	)
	if err != nil {
		t.Fatalf("seed realtime principals: %v", err)
	}
}

func migratedRealtimePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect to PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("realtime_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("create realtime pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire migration connection")
	}
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err == nil {
		_, err = migration.Apply(ctx, connection.Conn(), files)
	}
	connection.Release()
	if err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}
