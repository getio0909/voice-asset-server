package audio_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresOriginalRepositoryIsWorkspaceScoped(t *testing.T) {
	pool := migratedAudioPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "10000000-0000-4000-8000-000000000051"
		otherSpace  = "10000000-0000-4000-8000-000000000052"
		userID      = "20000000-0000-4000-8000-000000000051"
		assetID     = "30000000-0000-4000-8000-000000000051"
		objectID    = "40000000-0000-4000-8000-000000000051"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", workspaceID, otherSpace); err != nil {
		t.Fatalf("seed workspaces")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'owner@example.com', 'encoded', 'active')", userID); err != nil {
		t.Fatalf("seed user")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO assets (id, workspace_id, title, language, status, created_by) VALUES ($1, $2, 'Recording', 'en-US', 'ready', $3)", assetID, workspaceID, userID); err != nil {
		t.Fatalf("seed asset")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state
		) VALUES ($1, $2, 'original', 'local', 'objects/private/original.wav',
			'audio/wav', 12, repeat('a', 64), 'upload', 'none')`, objectID, assetID); err != nil {
		t.Fatalf("seed original object")
	}

	repository := audio.NewPostgresOriginalRepository(pool)
	got, err := repository.GetOriginal(ctx, workspaceID, assetID)
	if err != nil {
		t.Fatalf("GetOriginal() error = %v", err)
	}
	if got.AssetID != assetID || got.StorageBackend != storage.BackendLocal ||
		got.StorageKey != "objects/private/original.wav" ||
		got.MIMEType != "audio/wav" || got.Size != 12 || len(got.SHA256) != 64 {
		t.Fatalf("GetOriginal() = %+v", got)
	}
	if _, err := repository.GetOriginal(ctx, otherSpace, assetID); !errors.Is(err, audio.ErrAudioNotFound) {
		t.Fatalf("cross-workspace GetOriginal() error = %v, want ErrAudioNotFound", err)
	}
}

func migratedAudioPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("audio_test_%d", time.Now().UnixNano())
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
