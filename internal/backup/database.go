package backup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/jackc/pgx/v5"
)

func postgresCommandConnection(databaseURL string) (string, []string, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse PostgreSQL connection settings")
	}
	if strings.TrimSpace(config.Database) == "" || strings.TrimSpace(config.User) == "" {
		return "", nil, fmt.Errorf("PostgreSQL database and user are required")
	}
	parts := []string{
		"host=" + quoteConninfo(config.Host),
		"port=" + quoteConninfo(strconv.FormatUint(uint64(config.Port), 10)),
		"dbname=" + quoteConninfo(config.Database),
		"user=" + quoteConninfo(config.User),
		"sslmode=" + quoteConninfo(commandSSLMode(config)),
	}
	environment := []string{"PGPASSWORD=" + config.Password}
	return strings.Join(parts, " "), environment, nil
}

func commandSSLMode(config *pgx.ConnConfig) string {
	if config.TLSConfig == nil {
		for _, fallback := range config.Fallbacks {
			if fallback.TLSConfig != nil {
				return "allow"
			}
		}
		return "disable"
	}
	for _, fallback := range config.Fallbacks {
		if fallback.TLSConfig == nil {
			return "prefer"
		}
	}
	if config.TLSConfig.InsecureSkipVerify {
		return "require"
	}
	return "verify-full"
}

func quoteConninfo(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `\'`)
	return `'` + value + `'`
}

// PostgresDatabase inspects backup and restore databases with pgx.
type PostgresDatabase struct{}

// IsEmpty reports whether the target has no user relations or routines.
func (PostgresDatabase) IsEmpty(ctx context.Context, databaseURL string) (bool, error) {
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return false, fmt.Errorf("connect to target database")
	}
	defer connection.Close(context.WithoutCancel(ctx))
	var empty bool
	err = connection.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT 1
			FROM pg_class AS relation
			JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname NOT IN ('pg_catalog', 'information_schema')
			  AND namespace.nspname NOT LIKE 'pg_toast%'
			  AND relation.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
			UNION ALL
			SELECT 1
			FROM pg_proc AS routine
			JOIN pg_namespace AS namespace ON namespace.oid = routine.pronamespace
			WHERE namespace.nspname NOT IN ('pg_catalog', 'information_schema')
			  AND namespace.nspname NOT LIKE 'pg_toast%'
		)
	`).Scan(&empty)
	if err != nil {
		return false, fmt.Errorf("inspect target database")
	}
	return empty, nil
}

// LoadLocalObjects returns the stable local asset-object inventory.
func (PostgresDatabase) LoadLocalObjects(ctx context.Context, databaseURL string) ([]DatabaseObject, error) {
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database for object inventory")
	}
	defer connection.Close(context.WithoutCancel(ctx))
	rows, err := connection.Query(ctx, `
		SELECT storage_key, file_size, trim(sha256)
		FROM asset_objects
		WHERE storage_backend = 'local'
		ORDER BY storage_key
	`)
	if err != nil {
		return nil, fmt.Errorf("query local object inventory")
	}
	defer rows.Close()
	objects := make([]DatabaseObject, 0)
	for rows.Next() {
		var object DatabaseObject
		if err := rows.Scan(&object.StorageKey, &object.Size, &object.SHA256); err != nil {
			return nil, fmt.Errorf("scan local object inventory")
		}
		object.SHA256 = strings.TrimSpace(object.SHA256)
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read local object inventory")
	}
	return objects, nil
}

// LoadObjectInventory returns every durable object row plus unfinished upload
// parts for the selected backend. Upload parts inherit the configured backend
// because their historical table predates the backend column.
func (PostgresDatabase) LoadObjectInventory(
	ctx context.Context,
	databaseURL string,
	backend storage.Backend,
) ([]DatabaseObject, error) {
	if !backend.Valid() {
		return nil, fmt.Errorf("invalid storage backend %q", backend)
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database for object inventory")
	}
	defer connection.Close(context.WithoutCancel(ctx))
	rows, err := connection.Query(ctx, `
		SELECT storage_backend, storage_key, file_size, trim(sha256)
		FROM asset_objects
		UNION ALL
		SELECT $1::text, storage_key, size_bytes, trim(sha256)
		FROM upload_parts
		ORDER BY storage_backend, storage_key
	`, string(backend))
	if err != nil {
		return nil, fmt.Errorf("query object inventory")
	}
	defer rows.Close()
	objects := make([]DatabaseObject, 0)
	for rows.Next() {
		var object DatabaseObject
		if err := rows.Scan(&object.Backend, &object.StorageKey, &object.Size, &object.SHA256); err != nil {
			return nil, fmt.Errorf("scan object inventory")
		}
		object.Backend = strings.TrimSpace(object.Backend)
		object.SHA256 = strings.TrimSpace(object.SHA256)
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read object inventory")
	}
	return objects, nil
}
