package migration

import (
	"context"
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

	migrationDir := filepath.Join("..", "..", "..", "migrations")
	downSQL, err := os.ReadFile(filepath.Join(migrationDir, "000001_initial.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	resetSchema := func() {
		if _, err := conn.Exec(context.Background(), "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Errorf("reset test schema: %v", err)
		}
	}
	resetSchema()
	t.Cleanup(resetSchema)

	files, err := Load(migrationDir)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	applied, err := Apply(ctx, conn, files)
	if err != nil || applied != 1 {
		t.Fatalf("first Apply() = (%d, %v), want (1, nil)", applied, err)
	}
	applied, err = Apply(ctx, conn, files)
	if err != nil || applied != 0 {
		t.Fatalf("second Apply() = (%d, %v), want (0, nil)", applied, err)
	}

	tampered := append([]File(nil), files...)
	tampered[0].Checksum = strings.Repeat("0", 64)
	if _, err := Apply(ctx, conn, tampered); err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("tampered Apply() error = %v, want checksum rejection", err)
	}

	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("run down migration: %v", err)
	}
	var assetsTableExists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.assets') IS NOT NULL").Scan(&assetsTableExists); err != nil {
		t.Fatalf("query assets table: %v", err)
	}
	if assetsTableExists {
		t.Fatal("assets table remains after down migration")
	}
}
