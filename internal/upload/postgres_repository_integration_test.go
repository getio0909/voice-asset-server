package upload_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/upload"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryCreateIsIdempotentAndBindsAssetWorkspace(t *testing.T) {
	pool := migratedUploadPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "10000000-0000-4000-8000-000000000021"
		otherSpace  = "10000000-0000-4000-8000-000000000022"
		userID      = "20000000-0000-4000-8000-000000000021"
		assetID     = "30000000-0000-4000-8000-000000000021"
		sessionID   = "40000000-0000-4000-8000-000000000021"
		auditID     = "50000000-0000-4000-8000-000000000021"
		fileHash    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		requestHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", workspaceID, otherSpace); err != nil {
		t.Fatalf("seed workspaces")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, 'owner@example.com', 'encoded', 'active')`, userID); err != nil {
		t.Fatalf("seed user")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`, workspaceID, otherSpace, userID); err != nil {
		t.Fatalf("seed memberships")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets (id, workspace_id, title, language, status, created_by)
		VALUES ($1, $2, 'Recording', 'en', 'draft', $3)`, assetID, workspaceID, userID); err != nil {
		t.Fatalf("seed asset")
	}
	repository := upload.NewPostgresRepository(pool)
	params := upload.CreateParams{
		SessionID: sessionID, AuditID: auditID, WorkspaceID: workspaceID, CreatedBy: userID,
		AssetID: assetID, Filename: "recording.wav", MIMEType: "audio/wav",
		ExpectedSize: 1024, ExpectedSHA256: fileHash, PartSize: upload.DefaultPartSize,
		IdempotencyKey: "upload-key", RequestHash: requestHash,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	originalParams := params
	created, replayed, err := repository.Create(ctx, params)
	if err != nil || replayed || created.ID != sessionID || created.State != upload.StateActive {
		t.Fatalf("Create() = (%+v, %t, %v)", created, replayed, err)
	}
	params.SessionID = "40000000-0000-4000-8000-000000000099"
	params.AuditID = "50000000-0000-4000-8000-000000000099"
	replayedSession, replayed, err := repository.Create(ctx, params)
	if err != nil || !replayed || replayedSession.ID != sessionID {
		t.Fatalf("replayed Create() = (%+v, %t, %v)", replayedSession, replayed, err)
	}
	params.RequestHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, _, err := repository.Create(ctx, params); !errors.Is(err, upload.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Create() error = %v", err)
	}
	params.WorkspaceID = otherSpace
	params.IdempotencyKey = "other-key"
	params.RequestHash = requestHash
	if _, _, err := repository.Create(ctx, params); !errors.Is(err, upload.ErrNotFound) {
		t.Fatalf("cross-workspace Create() error = %v, want ErrNotFound", err)
	}
	var status string
	var version int64
	if err := pool.QueryRow(ctx, "SELECT status, version FROM assets WHERE id = $1", assetID).Scan(&status, &version); err != nil {
		t.Fatalf("query asset state")
	}
	if status != "uploading" || version != 2 {
		t.Fatalf("asset state = %s/%d, want uploading/2", status, version)
	}
	part := upload.Part{
		Number: 1, SizeBytes: 1024, SHA256: fileHash,
		StorageKey: "parts/test/upload-1.part",
	}
	recorded, replayedPart, err := repository.RecordPart(ctx, upload.RecordPartParams{
		WorkspaceID: workspaceID, UploadID: sessionID, Part: part,
	})
	if err != nil || replayedPart || recorded.Number != 1 {
		t.Fatalf("RecordPart() = (%+v, %t, %v)", recorded, replayedPart, err)
	}
	if _, replayedPart, err := repository.RecordPart(ctx, upload.RecordPartParams{
		WorkspaceID: workspaceID, UploadID: sessionID, Part: part,
	}); err != nil || !replayedPart {
		t.Fatalf("replayed RecordPart() = (%t, %v)", replayedPart, err)
	}
	if err := repository.MarkAssembling(ctx, workspaceID, sessionID); err != nil {
		t.Fatalf("MarkAssembling() error = %v", err)
	}
	completed, err := repository.Finish(ctx, upload.FinishParams{
		WorkspaceID:   workspaceID,
		UploadID:      sessionID,
		ActorID:       userID,
		AuditID:       "50000000-0000-4000-8000-000000000022",
		WaveformJobID: "70000000-0000-4000-8000-000000000021",
		Object: upload.OriginalObject{
			ID: "60000000-0000-4000-8000-000000000021", StorageBackend: storage.BackendS3,
			StorageKey: "objects/test/original",
			MIMEType:   "audio/wav", Container: "wav", Codec: "pcm_s16le",
			SampleRate: 16000, ChannelCount: 1, Bitrate: 256000, DurationMS: 32,
			FileSize: 1024, SHA256: fileHash,
		},
	})
	if err != nil || completed.State != upload.StateCompleted || completed.CompletedAt == nil {
		t.Fatalf("Finish() = (%+v, %v)", completed, err)
	}
	if err := pool.QueryRow(ctx, "SELECT status, version FROM assets WHERE id = $1", assetID).Scan(&status, &version); err != nil {
		t.Fatalf("query completed asset state")
	}
	if status != "ready" || version != 3 {
		t.Fatalf("completed asset state = %s/%d, want ready/3", status, version)
	}
	var storedBackend string
	if err := pool.QueryRow(ctx, "SELECT storage_backend FROM asset_objects WHERE id = $1", "60000000-0000-4000-8000-000000000021").Scan(&storedBackend); err != nil {
		t.Fatal("query completed object backend")
	}
	if storedBackend != string(storage.BackendS3) {
		t.Fatalf("storage backend = %q, want s3", storedBackend)
	}
	var waveformKind, waveformState, waveformPayloadAssetID string
	if err := pool.QueryRow(ctx, `
		SELECT kind, state, payload->>'asset_id'
		FROM jobs WHERE id = $1`, "70000000-0000-4000-8000-000000000021",
	).Scan(&waveformKind, &waveformState, &waveformPayloadAssetID); err != nil {
		t.Fatalf("query queued waveform job")
	}
	if waveformKind != "generate_waveform" || waveformState != "queued" || waveformPayloadAssetID != assetID {
		t.Fatalf("waveform job = %q/%q/%q", waveformKind, waveformState, waveformPayloadAssetID)
	}
	originalParams.SessionID = "40000000-0000-4000-8000-000000000098"
	originalParams.AuditID = "50000000-0000-4000-8000-000000000098"
	replayedCompleted, replayed, err := repository.Create(ctx, originalParams)
	if err != nil || !replayed || replayedCompleted.ID != sessionID || replayedCompleted.State != upload.StateCompleted {
		t.Fatalf("post-completion Create() replay = (%+v, %t, %v)", replayedCompleted, replayed, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE asset_objects SET storage_key = 'tampered' WHERE asset_id = $1 AND kind = 'original'", assetID); err == nil {
		t.Fatal("immutable original object accepted UPDATE")
	}
	if _, _, err := repository.RecordPart(ctx, upload.RecordPartParams{
		WorkspaceID: workspaceID, UploadID: sessionID, Part: part,
	}); !errors.Is(err, upload.ErrStateConflict) {
		t.Fatalf("post-completion RecordPart() error = %v, want ErrStateConflict", err)
	}
}

func migratedUploadPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("upload_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema")
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire migration connection")
	}
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		connection.Release()
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := migration.Apply(ctx, connection.Conn(), files); err != nil {
		connection.Release()
		t.Fatalf("apply migrations: %v", err)
	}
	connection.Release()
	return pool
}
