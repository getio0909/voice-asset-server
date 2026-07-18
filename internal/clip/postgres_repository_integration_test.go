package clip_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/clip"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcriptexport"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	artifactWorkspaceID = "10000000-0000-4000-8000-000000000071"
	artifactOtherSpace  = "10000000-0000-4000-8000-000000000072"
	artifactUserID      = "20000000-0000-4000-8000-000000000071"
	artifactAssetID     = "30000000-0000-4000-8000-000000000071"
	artifactOtherAsset  = "30000000-0000-4000-8000-000000000072"
	artifactOriginalID  = "40000000-0000-4000-8000-000000000071"
	artifactOtherObject = "40000000-0000-4000-8000-000000000072"
	artifactTranscript  = "50000000-0000-4000-8000-000000000071"
	artifactRevision    = "60000000-0000-4000-8000-000000000071"
	artifactAPIKeyID    = "70000000-0000-4000-8000-000000000071"
)

func TestAgentArtifactRepositoriesAreAtomicAuditedAndWorkspaceScoped(t *testing.T) {
	pool := migratedArtifactPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	seedArtifactFixtures(t, ctx, pool)
	expiresAt := time.Now().UTC().Add(time.Hour)

	clipRepository := clip.NewPostgresRepository(pool)
	createdClip, err := clipRepository.Create(ctx, clip.CreateParams{
		ID: "80000000-0000-4000-8000-000000000071", AuditID: "81000000-0000-4000-8000-000000000071",
		WorkspaceID: artifactWorkspaceID, AssetID: artifactAssetID, ParentObjectID: artifactOriginalID,
		ActorID: artifactUserID, ActorType: "agent", CredentialID: artifactAPIKeyID,
		RequestID: "artifact-create-clip", StorageBackend: storage.BackendS3,
		StorageKey: "objects/test/clip.wav",
		SHA256:     strings.Repeat("c", 64), StartMS: 500, EndMS: 2_000, DurationMS: 1_500,
		FileSize: 48_044, SampleRate: 16_000, ChannelCount: 1, Bitrate: 256_000,
		ExpiresAt: expiresAt,
	})
	if err != nil || createdClip.AssetID != artifactAssetID || createdClip.DurationMS != 1_500 {
		t.Fatalf("clip Create() = (%+v, %v)", createdClip, err)
	}
	storedClip, err := clipRepository.Get(ctx, artifactWorkspaceID, createdClip.ID, time.Now().UTC())
	if err != nil || storedClip.StorageBackend != storage.BackendS3 ||
		storedClip.StorageKey != "objects/test/clip.wav" || storedClip.DownloadURL == "" {
		t.Fatalf("clip Get() = (%+v, %v)", storedClip, err)
	}
	if _, err := clipRepository.Get(ctx, artifactOtherSpace, createdClip.ID, time.Now().UTC()); !errors.Is(err, clip.ErrNotFound) {
		t.Fatalf("clip cross-workspace Get() error = %v", err)
	}
	if _, err := clipRepository.Get(ctx, artifactWorkspaceID, createdClip.ID, expiresAt.Add(time.Second)); !errors.Is(err, clip.ErrNotFound) {
		t.Fatalf("expired clip Get() error = %v", err)
	}
	_, err = clipRepository.Create(ctx, clip.CreateParams{
		ID: "80000000-0000-4000-8000-000000000072", AuditID: "81000000-0000-4000-8000-000000000072",
		WorkspaceID: artifactWorkspaceID, AssetID: artifactOtherAsset, ParentObjectID: artifactOtherObject,
		ActorID: artifactUserID, ActorType: "agent", CredentialID: artifactAPIKeyID,
		RequestID: "artifact-cross-clip", StorageKey: "objects/test/cross-clip.wav",
		SHA256: strings.Repeat("d", 64), StartMS: 0, EndMS: 1_000, DurationMS: 1_000,
		FileSize: 32_044, SampleRate: 16_000, ChannelCount: 1, Bitrate: 256_000,
		ExpiresAt: expiresAt,
	})
	if !errors.Is(err, clip.ErrNotFound) {
		t.Fatalf("clip cross-workspace Create() error = %v", err)
	}

	exportRepository := transcriptexport.NewPostgresRepository(pool)
	createdExport, err := exportRepository.Create(ctx, transcriptexport.CreateParams{
		ID: "90000000-0000-4000-8000-000000000071", AuditID: "91000000-0000-4000-8000-000000000071",
		WorkspaceID: artifactWorkspaceID, AssetID: artifactAssetID, RevisionID: artifactRevision,
		ActorID: artifactUserID, ActorType: "agent", CredentialID: artifactAPIKeyID,
		RequestID: "artifact-create-export", Format: transcriptexport.FormatVTT,
		StorageBackend: storage.BackendS3,
		MIMEType:       "text/vtt; charset=utf-8", StorageKey: "objects/test/export.vtt",
		SHA256: strings.Repeat("e", 64), FileSize: 128, ExpiresAt: expiresAt,
	})
	if err != nil || createdExport.RevisionID != artifactRevision || createdExport.Format != transcriptexport.FormatVTT {
		t.Fatalf("export Create() = (%+v, %v)", createdExport, err)
	}
	storedExport, err := exportRepository.Get(ctx, artifactWorkspaceID, createdExport.ID, time.Now().UTC())
	if err != nil || storedExport.StorageBackend != storage.BackendS3 ||
		storedExport.StorageKey != "objects/test/export.vtt" || storedExport.DownloadURL == "" {
		t.Fatalf("export Get() = (%+v, %v)", storedExport, err)
	}
	if _, err := exportRepository.Get(ctx, artifactOtherSpace, createdExport.ID, time.Now().UTC()); !errors.Is(err, transcriptexport.ErrNotFound) {
		t.Fatalf("export cross-workspace Get() error = %v", err)
	}
	_, err = exportRepository.Create(ctx, transcriptexport.CreateParams{
		ID: "90000000-0000-4000-8000-000000000072", AuditID: "91000000-0000-4000-8000-000000000072",
		WorkspaceID: artifactOtherSpace, AssetID: artifactAssetID, RevisionID: artifactRevision,
		ActorID: artifactUserID, ActorType: "agent", CredentialID: artifactAPIKeyID,
		RequestID: "artifact-cross-export", Format: transcriptexport.FormatJSON,
		MIMEType: "application/json", StorageKey: "objects/test/cross-export.json",
		SHA256: strings.Repeat("f", 64), FileSize: 128, ExpiresAt: expiresAt,
	})
	if !errors.Is(err, transcriptexport.ErrNotFound) {
		t.Fatalf("export cross-workspace Create() error = %v", err)
	}

	for _, requestID := range []string{"artifact-create-clip", "artifact-create-export"} {
		var actorType, apiKeyID string
		if err := pool.QueryRow(ctx, `
			SELECT actor_type, metadata->>'api_key_id' FROM audit_logs WHERE request_id = $1`, requestID,
		).Scan(&actorType, &apiKeyID); err != nil || actorType != "agent" || apiKeyID != artifactAPIKeyID {
			t.Fatalf("audit %q = actor=%q api_key=%q error=%v", requestID, actorType, apiKeyID, err)
		}
	}
	var rejectedAuditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE request_id IN ('artifact-cross-clip', 'artifact-cross-export')`,
	).Scan(&rejectedAuditCount); err != nil || rejectedAuditCount != 0 {
		t.Fatalf("rejected audit count = (%d, %v)", rejectedAuditCount, err)
	}
}

func seedArtifactFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "workspaces and user",
			sql:  `INSERT INTO workspaces (id, name) VALUES ($1, 'Artifact primary'), ($2, 'Artifact other')`,
			args: []any{artifactWorkspaceID, artifactOtherSpace},
		},
		{
			name: "user",
			sql:  `INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'artifact-owner@example.com', 'encoded', 'active')`,
			args: []any{artifactUserID},
		},
		{
			name: "memberships",
			sql:  `INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`,
			args: []any{artifactWorkspaceID, artifactOtherSpace, artifactUserID},
		},
		{
			name: "assets",
			sql: `INSERT INTO assets (id, workspace_id, title, language, status, duration_ms, created_by)
				VALUES ($1, $3, 'Primary audio', 'en-US', 'ready', 10000, $5),
				       ($2, $4, 'Other audio', 'en-US', 'ready', 10000, $5)`,
			args: []any{artifactAssetID, artifactOtherAsset, artifactWorkspaceID, artifactOtherSpace, artifactUserID},
		},
		{
			name: "original objects",
			sql: `INSERT INTO asset_objects (
					id, asset_id, kind, storage_backend, storage_key, mime_type, container,
					codec, sample_rate, channel_count, bitrate, duration_ms, file_size,
					sha256, creation_source, encryption_state
				) VALUES
					($1, $3, 'original', 'local', 'objects/test/original.wav', 'audio/wav', 'wav',
					 'pcm_s16le', 16000, 1, 256000, 10000, 320044, repeat('a', 64), 'upload', 'none'),
					($2, $4, 'original', 'local', 'objects/test/other.wav', 'audio/wav', 'wav',
					 'pcm_s16le', 16000, 1, 256000, 10000, 320044, repeat('b', 64), 'upload', 'none')`,
			args: []any{artifactOriginalID, artifactOtherObject, artifactAssetID, artifactOtherAsset},
		},
		{
			name: "transcript",
			sql:  `INSERT INTO transcripts (id, asset_id, language) VALUES ($1, $2, 'en-US')`,
			args: []any{artifactTranscript, artifactAssetID},
		},
		{
			name: "revision",
			sql:  `INSERT INTO transcript_revisions (id, transcript_id, kind, text_content, created_by) VALUES ($1, $2, 'raw_asr', 'Artifact transcript', $3)`,
			args: []any{artifactRevision, artifactTranscript, artifactUserID},
		},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed artifact %s: %v", statement.name, err)
		}
	}
}

func migratedArtifactPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("artifact_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
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
