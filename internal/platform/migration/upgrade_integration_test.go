package migration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const expectedLatestMigrationVersion int64 = 18

func TestUpgradeFromEveryPriorVersion(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	files := loadUpgradeMigrations(t)
	if files[len(files)-1].Version != expectedLatestMigrationVersion {
		t.Fatalf("latest migration version = %d, want %d", files[len(files)-1].Version, expectedLatestMigrationVersion)
	}

	for prefixLength := 1; prefixLength < len(files); prefixLength++ {
		prefixLength := prefixLength
		t.Run(fmt.Sprintf("version_%d", files[prefixLength-1].Version), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			conn := isolatedUpgradeConnection(t, ctx, databaseURL, fmt.Sprintf("upgrade_v%d", prefixLength))
			applied, err := Apply(ctx, conn, files[:prefixLength])
			if err != nil || applied != prefixLength {
				t.Fatalf("apply prefix = (%d, %v), want (%d, nil)", applied, err, prefixLength)
			}
			applied, err = Apply(ctx, conn, files)
			if err != nil || applied != len(files)-prefixLength {
				t.Fatalf("upgrade to latest = (%d, %v), want (%d, nil)", applied, err, len(files)-prefixLength)
			}
			assertLatestMigrationSet(t, ctx, conn, len(files))
		})
	}
}

func TestSequentialUpgradePreservesLegacyData(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	files := loadUpgradeMigrations(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn := isolatedUpgradeConnection(t, ctx, databaseURL, "upgrade_data")

	applied, err := Apply(ctx, conn, files[:1])
	if err != nil || applied != 1 {
		t.Fatalf("apply initial migration = (%d, %v), want (1, nil)", applied, err)
	}
	seedVersionOneData(t, ctx, conn)

	applied, err = Apply(ctx, conn, files[:2])
	if err != nil || applied != 1 {
		t.Fatalf("upgrade to version 2 = (%d, %v), want (1, nil)", applied, err)
	}
	seedVersionTwoData(t, ctx, conn)

	applied, err = Apply(ctx, conn, files)
	if err != nil || applied != len(files)-2 {
		t.Fatalf("upgrade from version 2 to latest = (%d, %v), want (%d, nil)", applied, err, len(files)-2)
	}
	assertLatestMigrationSet(t, ctx, conn, len(files))
	assertLegacyDataPreserved(t, ctx, conn)
}

func TestWaveformMigrationBackfillsExistingOriginalsAndGuardsImmutability(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	files := loadUpgradeMigrations(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := isolatedUpgradeConnection(t, ctx, databaseURL, "upgrade_waveform")
	if applied, err := Apply(ctx, conn, files[:10]); err != nil || applied != 10 {
		t.Fatalf("apply through version 10 = (%d, %v), want (10, nil)", applied, err)
	}
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspaces (id, name) VALUES ($1, 'Waveform Workspace')`, []any{waveformWorkspaceID}},
		{`INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'waveform@example.test', 'hash', 'active')`, []any{waveformUserID}},
		{`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{waveformWorkspaceID, waveformUserID}},
		{`INSERT INTO assets (id, workspace_id, title, language, status, duration_ms, created_by) VALUES ($1, $2, 'Waveform source', 'en', 'ready', 1000, $3)`, []any{waveformAssetID, waveformWorkspaceID, waveformUserID}},
		{`INSERT INTO asset_objects (id, asset_id, kind, storage_backend, storage_key, mime_type, container, codec, duration_ms, file_size, sha256, creation_source, encryption_state) VALUES ($1, $2, 'original', 'local', 'objects/waveform/original.wav', 'audio/wav', 'wav', 'pcm_s16le', 1000, 64, repeat('a', 64), 'upload', 'none')`, []any{waveformOriginalID, waveformAssetID}},
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed pre-waveform data: %v", err)
		}
	}
	remaining := len(files) - 10
	if applied, err := Apply(ctx, conn, files); err != nil || applied != remaining {
		t.Fatalf("apply remaining migrations = (%d, %v), want (%d, nil)", applied, err, remaining)
	}
	assertLatestMigrationSet(t, ctx, conn, len(files))
	var jobID, state, createdBy, payloadAssetID string
	if err := conn.QueryRow(ctx, `
		SELECT id::text, state, created_by::text, payload->>'asset_id'
		FROM jobs WHERE asset_id = $1 AND kind = 'generate_waveform'`, waveformAssetID,
	).Scan(&jobID, &state, &createdBy, &payloadAssetID); err != nil {
		t.Fatalf("query backfilled waveform job: %v", err)
	}
	if jobID == "" || state != "queued" || createdBy != waveformUserID || payloadAssetID != waveformAssetID {
		t.Fatalf("backfilled waveform job = %q/%q/%q/%q", jobID, state, createdBy, payloadAssetID)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, parent_object_id, kind, storage_backend, storage_key,
			mime_type, container, codec, duration_ms, file_size, sha256,
			creation_source, encryption_state
		) VALUES (
			$1, $2, $3, 'waveform', 'local', 'derived/waveform.png',
			'image/png', 'png', 'png', 1000, 128, repeat('b', 64),
			'worker_waveform', 'none'
		)`, jobID, waveformAssetID, waveformOriginalID); err != nil {
		t.Fatalf("insert migrated waveform object: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE asset_objects SET storage_key = 'tampered' WHERE id = $1`, jobID); err == nil {
		t.Fatal("immutable waveform accepted UPDATE")
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, parent_object_id, kind, storage_backend, storage_key,
			mime_type, file_size, sha256, creation_source, encryption_state
		) VALUES ($1, $2, $3, 'waveform', 'local', 'derived/duplicate.png',
			'image/png', 128, repeat('c', 64), 'worker_waveform', 'none')`,
		waveformDuplicateID, waveformAssetID, waveformOriginalID); err == nil {
		t.Fatal("second waveform object bypassed unique index")
	}
}

func loadUpgradeMigrations(t *testing.T) []File {
	t.Helper()
	files, err := Load(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("load upgrade migrations: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("upgrade test requires at least two migrations, got %d", len(files))
	}
	return files
}

func isolatedUpgradeConnection(
	t *testing.T,
	ctx context.Context,
	databaseURL,
	prefix string,
) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect to upgrade-test PostgreSQL")
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	schema := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal("create isolated upgrade schema")
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+identifier); err != nil {
		t.Fatal("select isolated upgrade schema")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = conn.Exec(cleanupCtx, "SET search_path TO public")
		if _, err := conn.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated upgrade schema")
		}
	})
	return conn
}

func seedVersionOneData(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspaces (id, name) VALUES ($1, 'Legacy Workspace')`, []any{upgradeWorkspaceID}},
		{`INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'legacy@example.test', 'legacy-test-hash', 'active')`, []any{upgradeUserID}},
		{`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{upgradeWorkspaceID, upgradeUserID}},
		{`INSERT INTO assets (id, workspace_id, title, status, created_by) VALUES ($1, $2, 'Legacy recording', 'draft', $3)`, []any{upgradeAssetID, upgradeWorkspaceID, upgradeUserID}},
		{`INSERT INTO transcripts (id, asset_id, language) VALUES ($1, $2, 'en-US')`, []any{upgradeTranscriptID, upgradeAssetID}},
		{`INSERT INTO transcript_revisions (id, transcript_id, kind, text_content, created_by) VALUES ($1, $2, 'raw_asr', 'Legacy transcript.', $3)`, []any{upgradeRevisionID, upgradeTranscriptID, upgradeUserID}},
		{`INSERT INTO provider_profiles (id, workspace_id, provider_type, provider_id, display_name, config, state) VALUES ($1, $2, 'asr', 'mock_asr', 'Legacy Mock', '{}', 'enabled')`, []any{upgradeProviderID, upgradeWorkspaceID}},
		{`INSERT INTO system_settings (key, value) VALUES ('legacy.setting', '{"enabled":true}')`, nil},
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed version 1 legacy data: %v", err)
		}
	}
}

func seedVersionTwoData(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	if _, err := conn.Exec(ctx, `
		INSERT INTO sessions (id, user_id, workspace_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, clock_timestamp() + interval '1 day')`,
		upgradeSessionID, upgradeUserID, upgradeWorkspaceID, strings.Repeat("a", 64),
	); err != nil {
		t.Fatalf("seed version 2 session: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO jobs (
			id, workspace_id, asset_id, kind, state, attempts, available_at,
			created_by, payload, max_attempts, idempotency_key, idempotency_request_hash
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, 'mock_transcription', 'queued', 0, clock_timestamp(),
			$4::uuid, jsonb_build_object('asset_id', $3::uuid::text), 3, 'legacy-job', $5
		)`,
		upgradeJobID, upgradeWorkspaceID, upgradeAssetID, upgradeUserID, strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("seed version 2 job: %v", err)
	}
}

func assertLatestMigrationSet(t *testing.T, ctx context.Context, conn *pgx.Conn, expectedCount int) {
	t.Helper()
	var count int64
	var minimum, maximum int64
	if err := conn.QueryRow(ctx, `
		SELECT count(*), min(version), max(version)
		FROM voiceasset_schema_migrations`,
	).Scan(&count, &minimum, &maximum); err != nil {
		t.Fatalf("query applied migration set: %v", err)
	}
	if count != int64(expectedCount) || minimum != 1 || maximum != expectedLatestMigrationVersion {
		t.Fatalf("migration set = count:%d range:%d-%d, want count:%d range:1-%d", count, minimum, maximum, expectedCount, expectedLatestMigrationVersion)
	}
	var notificationsTableExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('notifications') IS NOT NULL`).Scan(&notificationsTableExists); err != nil {
		t.Fatalf("query upgraded notifications table: %v", err)
	}
	if !notificationsTableExists {
		t.Fatal("upgraded notifications table is missing")
	}
	var webhookTables int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM (VALUES
		    (to_regclass('webhook_endpoints')),
		    (to_regclass('webhook_deliveries'))
		) AS expected(table_name)
		WHERE table_name IS NOT NULL`).Scan(&webhookTables); err != nil {
		t.Fatalf("query upgraded webhook tables: %v", err)
	}
	if webhookTables != 2 {
		t.Fatalf("upgraded webhook table count = %d, want 2", webhookTables)
	}
}

func assertLegacyDataPreserved(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	var title, language string
	if err := conn.QueryRow(ctx, `SELECT title, language FROM assets WHERE id = $1`, upgradeAssetID).Scan(&title, &language); err != nil {
		t.Fatalf("load upgraded legacy asset: %v", err)
	}
	if title != "Legacy recording" || language != "und" {
		t.Fatalf("upgraded asset = %q/%q, want Legacy recording/und", title, language)
	}

	var transcriptText, createdByType, reviewStatus string
	if err := conn.QueryRow(ctx, `
		SELECT text_content, created_by_type, review_status
		FROM transcript_revisions WHERE id = $1`, upgradeRevisionID,
	).Scan(&transcriptText, &createdByType, &reviewStatus); err != nil {
		t.Fatalf("load upgraded legacy revision: %v", err)
	}
	if transcriptText != "Legacy transcript." || createdByType != "system" || reviewStatus != "pending" {
		t.Fatalf("upgraded revision = %q/%q/%q", transcriptText, createdByType, reviewStatus)
	}

	var providerName string
	var priority int
	var version int64
	if err := conn.QueryRow(ctx, `
		SELECT display_name, priority, version FROM provider_profiles WHERE id = $1`, upgradeProviderID,
	).Scan(&providerName, &priority, &version); err != nil {
		t.Fatalf("load upgraded legacy provider: %v", err)
	}
	if providerName != "Legacy Mock" || priority != 100 || version != 1 {
		t.Fatalf("upgraded provider = %q/%d/%d", providerName, priority, version)
	}

	var tokenHash, deviceName string
	var refreshIsNull, lastSeenValid bool
	if err := conn.QueryRow(ctx, `
		SELECT token_hash, device_name, refresh_token_hash IS NULL, last_seen_at >= created_at
		FROM sessions WHERE id = $1`, upgradeSessionID,
	).Scan(&tokenHash, &deviceName, &refreshIsNull, &lastSeenValid); err != nil {
		t.Fatalf("load upgraded legacy session: %v", err)
	}
	if tokenHash != strings.Repeat("a", 64) || deviceName != "Legacy session" || !refreshIsNull || !lastSeenValid {
		t.Fatalf("upgraded session = token_match:%t device:%q refresh_null:%t last_seen_valid:%t", tokenHash == strings.Repeat("a", 64), deviceName, refreshIsNull, lastSeenValid)
	}

	var jobState, createdBy string
	if err := conn.QueryRow(ctx, `SELECT state, created_by::text FROM jobs WHERE id = $1`, upgradeJobID).Scan(&jobState, &createdBy); err != nil {
		t.Fatalf("load upgraded legacy job: %v", err)
	}
	if jobState != "queued" || createdBy != upgradeUserID {
		t.Fatalf("upgraded job = %q/%q", jobState, createdBy)
	}

	var settingEnabled bool
	if err := conn.QueryRow(ctx, `SELECT (value->>'enabled')::boolean FROM system_settings WHERE key = 'legacy.setting'`).Scan(&settingEnabled); err != nil {
		t.Fatalf("load upgraded legacy setting: %v", err)
	}
	if !settingEnabled {
		t.Fatal("upgraded legacy setting is disabled")
	}

	var membershipStatus string
	var membershipVersion int64
	var membershipTimestampValid bool
	if err := conn.QueryRow(ctx, `
		SELECT status, version, updated_at >= created_at
		FROM memberships WHERE workspace_id = $1 AND user_id = $2`,
		upgradeWorkspaceID, upgradeUserID,
	).Scan(&membershipStatus, &membershipVersion, &membershipTimestampValid); err != nil {
		t.Fatalf("load upgraded legacy membership: %v", err)
	}
	if membershipStatus != "active" || membershipVersion != 1 || !membershipTimestampValid {
		t.Fatalf("upgraded membership = %q/%d/timestamp:%t", membershipStatus, membershipVersion, membershipTimestampValid)
	}
}

const (
	upgradeWorkspaceID  = "81000000-0000-4000-8000-000000000001"
	upgradeUserID       = "81000000-0000-4000-8000-000000000002"
	upgradeAssetID      = "81000000-0000-4000-8000-000000000003"
	upgradeTranscriptID = "81000000-0000-4000-8000-000000000004"
	upgradeRevisionID   = "81000000-0000-4000-8000-000000000005"
	upgradeProviderID   = "81000000-0000-4000-8000-000000000006"
	upgradeSessionID    = "81000000-0000-4000-8000-000000000007"
	upgradeJobID        = "81000000-0000-4000-8000-000000000008"
	waveformWorkspaceID = "82000000-0000-4000-8000-000000000001"
	waveformUserID      = "82000000-0000-4000-8000-000000000002"
	waveformAssetID     = "82000000-0000-4000-8000-000000000003"
	waveformOriginalID  = "82000000-0000-4000-8000-000000000004"
	waveformDuplicateID = "82000000-0000-4000-8000-000000000005"
)
