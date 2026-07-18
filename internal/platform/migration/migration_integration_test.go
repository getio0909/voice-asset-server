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

func TestApplyAgainstPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+identifier); err != nil {
		t.Fatalf("select isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "SET search_path TO public")
		if _, err := conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	})

	migrationDir := filepath.Join("..", "..", "..", "migrations")
	phaseEighteenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000018_outbound_webhooks.down.sql"))
	if err != nil {
		t.Fatalf("read outbound webhooks down migration: %v", err)
	}
	phaseSeventeenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000017_personal_notifications.down.sql"))
	if err != nil {
		t.Fatalf("read personal notifications down migration: %v", err)
	}
	phaseSixteenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000016_device_pairing.down.sql"))
	if err != nil {
		t.Fatalf("read device pairing down migration: %v", err)
	}
	phaseFifteenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000015_incremental_asset_sync.down.sql"))
	if err != nil {
		t.Fatalf("read incremental asset sync down migration: %v", err)
	}
	phaseFourteenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000014_membership_management.down.sql"))
	if err != nil {
		t.Fatalf("read membership management down migration: %v", err)
	}
	phaseThirteenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000013_realtime_recording_sessions.down.sql"))
	if err != nil {
		t.Fatalf("read realtime recording sessions down migration: %v", err)
	}
	phaseTwelveDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000012_asset_purge.down.sql"))
	if err != nil {
		t.Fatalf("read asset purge down migration: %v", err)
	}
	phaseElevenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000011_waveform_derivatives.down.sql"))
	if err != nil {
		t.Fatalf("read waveform derivatives down migration: %v", err)
	}
	phaseTenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000010_full_text_search.down.sql"))
	if err != nil {
		t.Fatalf("read full-text search down migration: %v", err)
	}
	phaseNineDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000009_asset_lifecycle.down.sql"))
	if err != nil {
		t.Fatalf("read asset lifecycle down migration: %v", err)
	}
	phaseEightDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000008_refresh_and_device_sessions.down.sql"))
	if err != nil {
		t.Fatalf("read refresh and device sessions down migration: %v", err)
	}
	phaseSevenDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000007_phase4_agent_artifacts.down.sql"))
	if err != nil {
		t.Fatalf("read Phase 4 agent artifacts down migration: %v", err)
	}
	phaseSixDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000006_phase4_organization_reads.down.sql"))
	if err != nil {
		t.Fatalf("read Phase 4 organization down migration: %v", err)
	}
	phaseFiveDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000005_phase4_api_keys.down.sql"))
	if err != nil {
		t.Fatalf("read Phase 4 API key down migration: %v", err)
	}
	phaseFourDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000004_phase3_llm_correction.down.sql"))
	if err != nil {
		t.Fatalf("read Phase 3 LLM down migration: %v", err)
	}
	phaseThreeDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000003_phase3_providers.down.sql"))
	if err != nil {
		t.Fatalf("read Phase 3 down migration: %v", err)
	}
	phaseOneDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000002_phase1_slice.down.sql"))
	if err != nil {
		t.Fatalf("read Phase 1 down migration: %v", err)
	}
	initialDownSQL, err := os.ReadFile(filepath.Join(migrationDir, "000001_initial.down.sql"))
	if err != nil {
		t.Fatalf("read initial down migration: %v", err)
	}
	files, err := Load(migrationDir)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	applied, err := Apply(ctx, conn, files)
	if err != nil || applied != len(files) {
		t.Fatalf("first Apply() = (%d, %v), want (%d, nil)", applied, err, len(files))
	}
	applied, err = Apply(ctx, conn, files)
	if err != nil || applied != 0 {
		t.Fatalf("second Apply() = (%d, %v), want (0, nil)", applied, err)
	}
	var searchIndexCount int
	searchIndexes := []string{
		"assets_search_vector_idx",
		"transcript_segments_search_vector_idx",
		"transcript_segments_speaker_revision_idx",
		"transcripts_asset_id_idx",
		"transcript_revisions_asr_provider_idx",
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname = ANY($1::text[])`, searchIndexes).Scan(&searchIndexCount); err != nil {
		t.Fatalf("query full-text search indexes: %v", err)
	}
	if searchIndexCount != len(searchIndexes) {
		t.Fatalf("full-text search index count = %d, want %d", searchIndexCount, len(searchIndexes))
	}
	var generatedColumnCount int
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND is_generated = 'ALWAYS'
		  AND (table_name, column_name) IN (
		    ('assets', 'search_vector'),
		    ('transcript_segments', 'search_vector')
		  )`).Scan(&generatedColumnCount); err != nil {
		t.Fatalf("query full-text search generated columns: %v", err)
	}
	if generatedColumnCount != 2 {
		t.Fatalf("full-text search generated column count = %d, want 2", generatedColumnCount)
	}
	var waveformIndexExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('asset_objects_one_waveform_per_asset') IS NOT NULL`).Scan(&waveformIndexExists); err != nil {
		t.Fatalf("query waveform unique index: %v", err)
	}
	if !waveformIndexExists {
		t.Fatal("waveform unique index is missing")
	}
	var recordingSessionsTableExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('recording_sessions') IS NOT NULL`).Scan(&recordingSessionsTableExists); err != nil {
		t.Fatalf("query recording sessions table: %v", err)
	}
	if !recordingSessionsTableExists {
		t.Fatal("recording sessions table is missing")
	}
	var syncChangesTableExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('sync_changes') IS NOT NULL`).Scan(&syncChangesTableExists); err != nil {
		t.Fatalf("query sync changes table: %v", err)
	}
	if !syncChangesTableExists {
		t.Fatal("sync changes table is missing")
	}
	var pairingSessionsTableExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('pairing_sessions') IS NOT NULL`).Scan(&pairingSessionsTableExists); err != nil {
		t.Fatalf("query pairing sessions table: %v", err)
	}
	if !pairingSessionsTableExists {
		t.Fatal("pairing sessions table is missing")
	}
	var notificationsTableExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('notifications') IS NOT NULL`).Scan(&notificationsTableExists); err != nil {
		t.Fatalf("query notifications table: %v", err)
	}
	if !notificationsTableExists {
		t.Fatal("notifications table is missing")
	}
	var webhookTableCount int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM (VALUES
		    (to_regclass('webhook_endpoints')),
		    (to_regclass('webhook_deliveries'))
		) AS expected(table_name)
		WHERE table_name IS NOT NULL`).Scan(&webhookTableCount); err != nil {
		t.Fatalf("query outbound webhook tables: %v", err)
	}
	if webhookTableCount != 2 {
		t.Fatalf("outbound webhook table count = %d, want 2", webhookTableCount)
	}

	tampered := append([]File(nil), files...)
	tampered[0].Checksum = strings.Repeat("0", 64)
	if _, err := Apply(ctx, conn, tampered); err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("tampered Apply() error = %v, want checksum rejection", err)
	}

	if _, err := conn.Exec(ctx, string(phaseEighteenDownSQL)); err != nil {
		t.Fatalf("run outbound webhooks down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseSeventeenDownSQL)); err != nil {
		t.Fatalf("run personal notifications down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseSixteenDownSQL)); err != nil {
		t.Fatalf("run device pairing down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseFifteenDownSQL)); err != nil {
		t.Fatalf("run incremental asset sync down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseFourteenDownSQL)); err != nil {
		t.Fatalf("run membership management down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseThirteenDownSQL)); err != nil {
		t.Fatalf("run realtime recording sessions down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseTwelveDownSQL)); err != nil {
		t.Fatalf("run asset purge down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseElevenDownSQL)); err != nil {
		t.Fatalf("run waveform derivatives down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseTenDownSQL)); err != nil {
		t.Fatalf("run full-text search down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseNineDownSQL)); err != nil {
		t.Fatalf("run asset lifecycle down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseEightDownSQL)); err != nil {
		t.Fatalf("run refresh and device sessions down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseSevenDownSQL)); err != nil {
		t.Fatalf("run Phase 4 agent artifacts down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseSixDownSQL)); err != nil {
		t.Fatalf("run Phase 4 organization down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseFiveDownSQL)); err != nil {
		t.Fatalf("run Phase 4 API key down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseFourDownSQL)); err != nil {
		t.Fatalf("run Phase 3 LLM down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseThreeDownSQL)); err != nil {
		t.Fatalf("run Phase 3 down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(phaseOneDownSQL)); err != nil {
		t.Fatalf("run Phase 1 down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(initialDownSQL)); err != nil {
		t.Fatalf("run initial down migration: %v", err)
	}
	var assetsTableExists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('assets') IS NOT NULL").Scan(&assetsTableExists); err != nil {
		t.Fatalf("query assets table: %v", err)
	}
	if assetsTableExists {
		t.Fatal("assets table remains after down migration")
	}
}
