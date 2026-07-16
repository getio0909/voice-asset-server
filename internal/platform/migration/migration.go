// Package migration loads and applies ordered PostgreSQL schema migrations.
package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const advisoryLockID int64 = 0x564F494345415353 // "VOICEASS"

var migrationFilename = regexp.MustCompile(`^([0-9]+)_([a-z0-9_]+)\.up\.sql$`)

// File is one immutable, ordered migration.
type File struct {
	Version  int64
	Name     string
	Path     string
	SQL      string
	Checksum string
}

// Load reads valid *.up.sql migrations from dir in ascending version order.
func Load(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory %q: %w", dir, err)
	}

	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		parts := migrationFilename.FindStringSubmatch(entry.Name())
		if parts == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", path, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			return nil, fmt.Errorf("migration %q is empty", path)
		}
		digest := sha256.Sum256(content)
		files = append(files, File{
			Version:  version,
			Name:     parts[2],
			Path:     path,
			SQL:      string(content),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no up migrations found in %q", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })
	for index := 1; index < len(files); index++ {
		if files[index-1].Version == files[index].Version {
			return nil, fmt.Errorf("duplicate migration version %d", files[index].Version)
		}
	}
	return files, nil
}

// Apply executes all unapplied migrations transactionally and verifies the
// checksums of migrations already recorded by this database.
func Apply(ctx context.Context, conn *pgx.Conn, files []File) (int, error) {
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return 0, fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockID)
	}()

	if _, err := conn.Exec(ctx, `
CREATE TABLE IF NOT EXISTS voiceasset_schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum char(64) NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`); err != nil {
		return 0, fmt.Errorf("ensure schema migration table: %w", err)
	}

	applied, err := appliedChecksums(ctx, conn)
	if err != nil {
		return 0, err
	}
	appliedCount := 0
	for _, file := range files {
		if checksum, ok := applied[file.Version]; ok {
			if checksum != file.Checksum {
				return appliedCount, fmt.Errorf(
					"migration %d checksum changed: database=%s file=%s",
					file.Version,
					checksum,
					file.Checksum,
				)
			}
			continue
		}

		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, file.SQL); err != nil {
				return fmt.Errorf("execute migration %d_%s: %w", file.Version, file.Name, err)
			}
			if _, err := tx.Exec(ctx,
				"INSERT INTO voiceasset_schema_migrations(version, name, checksum) VALUES ($1, $2, $3)",
				file.Version,
				file.Name,
				file.Checksum,
			); err != nil {
				return fmt.Errorf("record migration %d: %w", file.Version, err)
			}
			return nil
		})
		if err != nil {
			return appliedCount, err
		}
		appliedCount++
	}
	return appliedCount, nil
}

func appliedChecksums(ctx context.Context, conn *pgx.Conn) (map[int64]string, error) {
	rows, err := conn.Query(ctx, "SELECT version, checksum FROM voiceasset_schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]string)
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = strings.TrimSpace(checksum)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}
