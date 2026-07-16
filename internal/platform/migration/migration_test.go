package migration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrdersAndHashesMigrations(t *testing.T) {
	dir := t.TempDir()
	writeTestMigration(t, dir, "000002_add_jobs.up.sql", "SELECT 2;")
	writeTestMigration(t, dir, "000001_initial.up.sql", "SELECT 1;")
	writeTestMigration(t, dir, "000001_initial.down.sql", "SELECT 0;")

	files, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(files))
	}
	if files[0].Version != 1 || files[0].Name != "initial" || len(files[0].Checksum) != 64 {
		t.Fatalf("unexpected first migration: %+v", files[0])
	}
	if files[1].Version != 2 {
		t.Fatalf("second version = %d, want 2", files[1].Version)
	}
}

func TestLoadRejectsDuplicateVersions(t *testing.T) {
	dir := t.TempDir()
	writeTestMigration(t, dir, "000001_initial.up.sql", "SELECT 1;")
	writeTestMigration(t, dir, "000001_duplicate.up.sql", "SELECT 2;")

	if _, err := Load(dir); err == nil {
		t.Fatal("Load() error = nil, want duplicate version error")
	}
}

func writeTestMigration(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write migration: %v", err)
	}
}
