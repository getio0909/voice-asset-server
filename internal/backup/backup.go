// Package backup creates, verifies, and restores offline VoiceAsset backups.
package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	ManifestSchemaVersion       = 2
	legacyManifestSchemaVersion = 1
	manifestFilename            = "manifest.json"
	manifestDigestName          = "manifest.sha256"
	databaseFilename            = "database.dump"
	storageDirectory            = "objects"
	maxManifestBytes            = 64 << 20
	maxCommandErrorBytes        = 8 << 10
)

// FileArtifact records one immutable file in a backup.
type FileArtifact struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// DatabaseObject records the local object referenced by one asset_objects row.
type DatabaseObject struct {
	Backend    string `json:"backend,omitempty"`
	StorageKey string `json:"storage_key"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

// Manifest is the versioned, portable backup inventory.
type Manifest struct {
	SchemaVersion   int              `json:"schema_version"`
	CreatedAt       time.Time        `json:"created_at"`
	ServerVersion   string           `json:"server_version"`
	ContractVersion string           `json:"contract_version"`
	StorageBackend  string           `json:"storage_backend"`
	Database        FileArtifact     `json:"database"`
	StorageFiles    []FileArtifact   `json:"storage_files"`
	DatabaseObjects []DatabaseObject `json:"database_objects"`
}

// Summary is safe to emit from an operator command.
type Summary struct {
	Path            string    `json:"path"`
	CreatedAt       time.Time `json:"created_at"`
	ServerVersion   string    `json:"server_version"`
	ContractVersion string    `json:"contract_version"`
	StorageFiles    int       `json:"storage_files"`
	DatabaseObjects int       `json:"database_objects"`
	TotalBytes      int64     `json:"total_bytes"`
}

// Command describes one credential-safe PostgreSQL client invocation.
type Command struct {
	Name   string
	Args   []string
	Env    []string
	Stdout io.Writer
	Stderr io.Writer
}

// CommandRunner executes PostgreSQL client commands.
type CommandRunner interface {
	Run(context.Context, Command) error
}

// ExecRunner executes commands without a shell.
type ExecRunner struct{}

// Run implements CommandRunner.
func (ExecRunner) Run(ctx context.Context, command Command) error {
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Env = append(os.Environ(), command.Env...)
	process.Stdout = command.Stdout
	process.Stderr = command.Stderr
	return process.Run()
}

// Database provides the database checks needed for a consistent backup.
type Database interface {
	IsEmpty(context.Context, string) (bool, error)
	LoadLocalObjects(context.Context, string) ([]DatabaseObject, error)
}

// ObjectInventoryDatabase optionally exposes both local and S3 object rows.
// The backend argument also supplies the storage backend used by upload parts,
// whose historical table does not persist a backend column.
type ObjectInventoryDatabase interface {
	LoadObjectInventory(context.Context, string, storage.Backend) ([]DatabaseObject, error)
}

// SnapshotStorage is the storage boundary needed by S3 backup and restore.
// It is deliberately narrower than the request-path driver and only handles
// verified immutable snapshots plus an empty-target check.
type SnapshotStorage interface {
	Backend() storage.Backend
	Open(context.Context, string) (storage.File, error)
	PutSnapshot(context.Context, string, io.Reader, int64, string) (storage.Object, error)
	ListKeys(context.Context, string, int) ([]string, error)
	DeleteObject(context.Context, string, int64, string) error
}

// CreateOptions controls an offline backup.
type CreateOptions struct {
	DatabaseURL     string
	StorageRoot     string
	Destination     string
	PGDumpPath      string
	ServerVersion   string
	ContractVersion string
	ConfirmOffline  bool
	Runner          CommandRunner
	Database        Database
	Storage         SnapshotStorage
	Now             func() time.Time
}

// VerifyOptions controls backup verification.
type VerifyOptions struct {
	BackupPath    string
	PGRestorePath string
	Runner        CommandRunner
}

// RestoreOptions controls restore into a clean database and storage root.
type RestoreOptions struct {
	BackupPath         string
	DatabaseURL        string
	StorageRoot        string
	PGRestorePath      string
	ConfirmEmptyTarget bool
	Runner             CommandRunner
	Database           Database
	TargetStorage      SnapshotStorage
}

type verifiedBackup struct {
	root     string
	manifest Manifest
	summary  Summary
}

// Create writes a complete backup to a new directory and publishes it by one
// same-filesystem rename. Callers must quiesce API, worker, and Agent writes.
func Create(ctx context.Context, options CreateOptions) (Summary, error) {
	if ctx == nil {
		return Summary{}, fmt.Errorf("backup context is required")
	}
	if !options.ConfirmOffline {
		return Summary{}, fmt.Errorf("offline confirmation is required before backup")
	}
	if strings.TrimSpace(options.DatabaseURL) == "" {
		return Summary{}, fmt.Errorf("database URL is required")
	}
	if strings.TrimSpace(options.ServerVersion) == "" || strings.TrimSpace(options.ContractVersion) == "" {
		return Summary{}, fmt.Errorf("server and contract versions are required")
	}
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	database := options.Database
	if database == nil {
		database = PostgresDatabase{}
	}
	pgDump := strings.TrimSpace(options.PGDumpPath)
	if pgDump == "" {
		pgDump = "pg_dump"
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	backend := storage.BackendLocal
	snapshotStore := options.Storage
	if snapshotStore != nil {
		backend = snapshotStore.Backend()
	}
	if !backend.Valid() {
		return Summary{}, fmt.Errorf("invalid storage backend %q", backend)
	}
	var storageRoot string
	var err error
	if backend == storage.BackendLocal {
		storageRoot, err = existingRealDirectory(options.StorageRoot, "storage root")
		if err != nil {
			return Summary{}, err
		}
	} else if snapshotStore == nil {
		return Summary{}, fmt.Errorf("S3 backup requires a configured snapshot storage")
	}
	destination, parent, err := newDirectoryPath(options.Destination)
	if err != nil {
		return Summary{}, err
	}
	if storageRoot != "" && pathsOverlap(storageRoot, destination) {
		return Summary{}, fmt.Errorf("backup destination and storage root must not overlap")
	}

	var databaseObjects []DatabaseObject
	if backend == storage.BackendS3 {
		inventory, ok := database.(ObjectInventoryDatabase)
		if !ok {
			return Summary{}, fmt.Errorf("S3 backup requires database object inventory support")
		}
		databaseObjects, err = inventory.LoadObjectInventory(ctx, options.DatabaseURL, backend)
	} else {
		databaseObjects, err = database.LoadLocalObjects(ctx, options.DatabaseURL)
	}
	if err != nil {
		return Summary{}, err
	}
	databaseObjects, err = normalizeDatabaseObjects(databaseObjects, backend)
	if err != nil {
		return Summary{}, fmt.Errorf("validate database object inventory: %w", err)
	}

	partial, err := os.MkdirTemp(parent, "."+filepath.Base(destination)+".partial-")
	if err != nil {
		return Summary{}, fmt.Errorf("create backup staging directory: %w", err)
	}
	if err := os.Chmod(partial, 0o700); err != nil {
		_ = os.RemoveAll(partial)
		return Summary{}, fmt.Errorf("protect backup staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(partial)
		}
	}()

	databasePath := filepath.Join(partial, databaseFilename)
	connection, connectionEnvironment, err := postgresCommandConnection(options.DatabaseURL)
	if err != nil {
		return Summary{}, err
	}
	if err := runExternalCommand(ctx, runner, Command{
		Name: pgDump,
		Args: []string{
			"--dbname", connection, "--format=custom", "--no-owner", "--no-privileges", "--file", databasePath,
		},
		Env: connectionEnvironment, Stdout: io.Discard,
	}, options.DatabaseURL, passwordFromEnvironment(connectionEnvironment)); err != nil {
		return Summary{}, fmt.Errorf("create PostgreSQL backup: %w", err)
	}
	databaseArtifact, err := inspectRegularFile(databasePath, databaseFilename)
	if err != nil {
		return Summary{}, fmt.Errorf("inspect PostgreSQL backup: %w", err)
	}

	storageTarget := filepath.Join(partial, storageDirectory)
	var storageFiles []FileArtifact
	if backend == storage.BackendS3 {
		storageFiles, err = copySnapshotObjects(ctx, snapshotStore, databaseObjects, storageTarget)
	} else {
		storageFiles, err = copyStorageTree(ctx, storageRoot, storageTarget)
	}
	if err != nil {
		return Summary{}, err
	}
	if err := matchDatabaseObjects(databaseObjects, storageFiles, backend); err != nil {
		return Summary{}, fmt.Errorf("database/object consistency check: %w", err)
	}

	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, CreatedAt: now().UTC().Truncate(time.Second),
		ServerVersion: strings.TrimSpace(options.ServerVersion), ContractVersion: strings.TrimSpace(options.ContractVersion),
		StorageBackend: string(backend),
		Database:       databaseArtifact, StorageFiles: storageFiles, DatabaseObjects: databaseObjects,
	}
	manifestPath := filepath.Join(partial, manifestFilename)
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		return Summary{}, fmt.Errorf("write backup manifest: %w", err)
	}
	manifestArtifact, err := inspectRegularFile(manifestPath, manifestFilename)
	if err != nil {
		return Summary{}, fmt.Errorf("hash backup manifest: %w", err)
	}
	if err := writeExclusiveFile(
		filepath.Join(partial, manifestDigestName),
		[]byte(manifestArtifact.SHA256+"  "+manifestFilename+"\n"),
	); err != nil {
		return Summary{}, fmt.Errorf("write manifest checksum: %w", err)
	}
	if err := syncDirectory(partial); err != nil {
		return Summary{}, fmt.Errorf("sync backup staging directory: %w", err)
	}
	if err := os.Rename(partial, destination); err != nil {
		return Summary{}, fmt.Errorf("publish backup directory: %w", err)
	}
	published = true
	if err := syncDirectory(parent); err != nil {
		return Summary{}, fmt.Errorf("sync backup parent directory: %w", err)
	}
	return summarize(destination, manifest), nil
}

// Verify validates the manifest, every byte, the database archive structure,
// and every local database object reference.
func Verify(ctx context.Context, options VerifyOptions) (Summary, error) {
	verified, err := verify(ctx, options)
	if err != nil {
		return Summary{}, err
	}
	return verified.summary, nil
}

// Restore verifies a backup, refuses non-empty targets, restores the database
// in one pg_restore transaction, checks its object inventory, and publishes the
// storage tree by rename.
func Restore(ctx context.Context, options RestoreOptions) (Summary, error) {
	if ctx == nil {
		return Summary{}, fmt.Errorf("restore context is required")
	}
	if !options.ConfirmEmptyTarget {
		return Summary{}, fmt.Errorf("empty-target confirmation is required before restore")
	}
	if strings.TrimSpace(options.DatabaseURL) == "" {
		return Summary{}, fmt.Errorf("target database URL is required")
	}
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	database := options.Database
	if database == nil {
		database = PostgresDatabase{}
	}
	pgRestore := strings.TrimSpace(options.PGRestorePath)
	if pgRestore == "" {
		pgRestore = "pg_restore"
	}
	verified, err := verify(ctx, VerifyOptions{
		BackupPath: options.BackupPath, PGRestorePath: pgRestore, Runner: runner,
	})
	if err != nil {
		return Summary{}, err
	}
	backend := manifestStorageBackend(verified.manifest)
	var targetStorage SnapshotStorage
	if backend != storage.BackendLocal {
		targetStorage = options.TargetStorage
		if targetStorage == nil || targetStorage.Backend() != backend {
			return Summary{}, fmt.Errorf("restore requires a configured %s target storage", backend)
		}
		keys, err := targetStorage.ListKeys(ctx, "", 1)
		if err != nil {
			return Summary{}, fmt.Errorf("inspect target %s storage: %w", backend, err)
		}
		if len(keys) != 0 {
			return Summary{}, fmt.Errorf("target %s storage contains user objects; restore requires a clean target", backend)
		}
	}
	empty, err := database.IsEmpty(ctx, options.DatabaseURL)
	if err != nil {
		return Summary{}, err
	}
	if !empty {
		return Summary{}, fmt.Errorf("target database contains user objects; restore requires a clean database")
	}

	target, parent, existed, err := emptyTargetDirectory(options.StorageRoot)
	if err != nil {
		return Summary{}, err
	}
	if pathsOverlap(verified.root, target) {
		return Summary{}, fmt.Errorf("backup and target storage paths must not overlap")
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".restore-")
	if err != nil {
		return Summary{}, fmt.Errorf("create restore staging directory: %w", err)
	}
	if err := os.Chmod(stage, 0o700); err != nil {
		_ = os.RemoveAll(stage)
		return Summary{}, fmt.Errorf("protect restore staging directory: %w", err)
	}
	published := false
	uploaded := make([]DatabaseObject, 0)
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
			if targetStorage != nil {
				for _, object := range uploaded {
					_ = targetStorage.DeleteObject(context.Background(), object.StorageKey, object.Size, object.SHA256)
				}
			}
		}
	}()
	for _, artifact := range verified.manifest.StorageFiles {
		relative := strings.TrimPrefix(artifact.Path, storageDirectory+"/")
		source, err := secureJoin(verified.root, artifact.Path)
		if err != nil {
			return Summary{}, err
		}
		destination, err := secureJoin(stage, relative)
		if err != nil {
			return Summary{}, err
		}
		copied, err := copyRegularFile(ctx, source, destination, relative)
		if err != nil {
			return Summary{}, fmt.Errorf("stage restored object %q: %w", relative, err)
		}
		if copied.Size != artifact.Size || copied.SHA256 != artifact.SHA256 {
			return Summary{}, fmt.Errorf("restored object %q failed its manifest checksum", relative)
		}
	}
	if targetStorage != nil {
		var restored []DatabaseObject
		restored, err = restoreSnapshotObjects(ctx, targetStorage, verified.manifest, stage)
		uploaded = append(uploaded, restored...)
		if err != nil {
			return Summary{}, err
		}
	}

	databaseDump, err := secureJoin(verified.root, verified.manifest.Database.Path)
	if err != nil {
		return Summary{}, err
	}
	connection, connectionEnvironment, err := postgresCommandConnection(options.DatabaseURL)
	if err != nil {
		return Summary{}, err
	}
	if err := runExternalCommand(ctx, runner, Command{
		Name: pgRestore,
		Args: []string{
			"--dbname", connection, "--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges", databaseDump,
		},
		Env: connectionEnvironment, Stdout: io.Discard,
	}, options.DatabaseURL, passwordFromEnvironment(connectionEnvironment)); err != nil {
		return Summary{}, fmt.Errorf("restore PostgreSQL backup: %w", err)
	}
	var restoredObjects []DatabaseObject
	if backend == storage.BackendLocal {
		restoredObjects, err = database.LoadLocalObjects(ctx, options.DatabaseURL)
	} else if inventory, ok := database.(ObjectInventoryDatabase); ok {
		restoredObjects, err = inventory.LoadObjectInventory(ctx, options.DatabaseURL, backend)
	} else {
		return Summary{}, fmt.Errorf("%s restore requires database object inventory support", backend)
	}
	if err != nil {
		return Summary{}, err
	}
	if err := compareDatabaseObjects(verified.manifest.DatabaseObjects, restoredObjects, backend); err != nil {
		return Summary{}, fmt.Errorf("restored database object inventory: %w", err)
	}
	if targetStorage != nil {
		if err := os.RemoveAll(stage); err != nil {
			return Summary{}, fmt.Errorf("remove staged S3 restore: %w", err)
		}
		if existed {
			if err := os.Remove(target); err != nil {
				return Summary{}, fmt.Errorf("remove S3 restore staging root: %w", err)
			}
		}
		published = true
		if err := syncDirectory(parent); err != nil {
			return Summary{}, fmt.Errorf("sync S3 restore staging parent: %w", err)
		}
		result := verified.summary
		result.Path = target
		return result, nil
	}
	if existed {
		if err := os.Remove(target); err != nil {
			return Summary{}, fmt.Errorf("remove empty target storage directory: %w", err)
		}
	}
	if err := os.Rename(stage, target); err != nil {
		if existed {
			_ = os.Mkdir(target, 0o700)
		}
		return Summary{}, fmt.Errorf("publish restored storage: %w", err)
	}
	published = true
	if err := syncDirectory(parent); err != nil {
		return Summary{}, fmt.Errorf("sync restored storage parent: %w", err)
	}
	result := verified.summary
	result.Path = target
	return result, nil
}

func verify(ctx context.Context, options VerifyOptions) (verifiedBackup, error) {
	if ctx == nil {
		return verifiedBackup{}, fmt.Errorf("verification context is required")
	}
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	pgRestore := strings.TrimSpace(options.PGRestorePath)
	if pgRestore == "" {
		pgRestore = "pg_restore"
	}
	root, err := existingRealDirectory(options.BackupPath, "backup directory")
	if err != nil {
		return verifiedBackup{}, err
	}
	manifestPath := filepath.Join(root, manifestFilename)
	checksumPath := filepath.Join(root, manifestDigestName)
	checksumArtifact, err := inspectRegularFile(checksumPath, manifestDigestName)
	if err != nil {
		return verifiedBackup{}, fmt.Errorf("inspect manifest checksum: %w", err)
	}
	if checksumArtifact.Size > 4096 {
		return verifiedBackup{}, fmt.Errorf("manifest checksum file exceeds 4096 bytes")
	}
	checksumBytes, err := readBounded(checksumPath, 4096)
	if err != nil {
		return verifiedBackup{}, fmt.Errorf("read manifest checksum: %w", err)
	}
	fields := strings.Fields(string(checksumBytes))
	if len(fields) != 2 || fields[1] != manifestFilename || !validSHA256(fields[0]) {
		return verifiedBackup{}, fmt.Errorf("manifest checksum file is invalid")
	}
	manifestArtifact, err := inspectRegularFile(manifestPath, manifestFilename)
	if err != nil {
		return verifiedBackup{}, fmt.Errorf("inspect backup manifest: %w", err)
	}
	if manifestArtifact.SHA256 != fields[0] {
		return verifiedBackup{}, fmt.Errorf("backup manifest checksum mismatch")
	}
	manifestBytes, err := readBounded(manifestPath, maxManifestBytes)
	if err != nil {
		return verifiedBackup{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return verifiedBackup{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return verifiedBackup{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return verifiedBackup{}, err
	}

	expected := map[string]FileArtifact{
		manifestFilename:       manifestArtifact,
		manifestDigestName:     {Path: manifestDigestName},
		manifest.Database.Path: manifest.Database,
	}
	for _, artifact := range manifest.StorageFiles {
		if _, duplicate := expected[artifact.Path]; duplicate {
			return verifiedBackup{}, fmt.Errorf("backup manifest contains duplicate path %q", artifact.Path)
		}
		expected[artifact.Path] = artifact
	}
	if err := rejectUnexpectedFiles(root, expected); err != nil {
		return verifiedBackup{}, err
	}
	for name, artifact := range expected {
		if name == manifestDigestName {
			continue
		}
		filePath, err := secureJoin(root, name)
		if err != nil {
			return verifiedBackup{}, err
		}
		actual, err := inspectRegularFile(filePath, name)
		if err != nil {
			return verifiedBackup{}, err
		}
		if actual.Size != artifact.Size || actual.SHA256 != artifact.SHA256 {
			return verifiedBackup{}, fmt.Errorf("backup file %q failed its manifest checksum", name)
		}
	}
	if err := matchDatabaseObjects(manifest.DatabaseObjects, manifest.StorageFiles, manifestStorageBackend(manifest)); err != nil {
		return verifiedBackup{}, fmt.Errorf("manifest database/object consistency: %w", err)
	}
	databasePath, err := secureJoin(root, manifest.Database.Path)
	if err != nil {
		return verifiedBackup{}, err
	}
	if err := runExternalCommand(ctx, runner, Command{
		Name: pgRestore, Args: []string{"--list", databasePath},
		Stdout: io.Discard,
	}); err != nil {
		return verifiedBackup{}, fmt.Errorf("validate PostgreSQL archive: %w", err)
	}
	return verifiedBackup{root: root, manifest: manifest, summary: summarize(root, manifest)}, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion && manifest.SchemaVersion != legacyManifestSchemaVersion {
		return fmt.Errorf("unsupported backup manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.CreatedAt.IsZero() || strings.TrimSpace(manifest.ServerVersion) == "" ||
		strings.TrimSpace(manifest.ContractVersion) == "" {
		return fmt.Errorf("backup manifest identity is incomplete")
	}
	backend := manifestStorageBackend(manifest)
	if !backend.Valid() {
		return fmt.Errorf("backup storage backend %q is invalid", backend)
	}
	if manifest.Database.Path != databaseFilename || manifest.Database.Size < 0 ||
		!validSHA256(manifest.Database.SHA256) {
		return fmt.Errorf("backup database artifact is invalid")
	}
	if manifest.StorageFiles == nil || manifest.DatabaseObjects == nil {
		return fmt.Errorf("backup manifest inventories are required")
	}
	previous := ""
	for _, artifact := range manifest.StorageFiles {
		if err := validateBackupPath(artifact.Path); err != nil ||
			!strings.HasPrefix(artifact.Path, storageDirectory+"/") || artifact.Size < 0 ||
			!validSHA256(artifact.SHA256) {
			return fmt.Errorf("backup storage artifact %q is invalid", artifact.Path)
		}
		if previous != "" && artifact.Path <= previous {
			return fmt.Errorf("backup storage artifacts are not unique and sorted")
		}
		previous = artifact.Path
	}
	_, err := normalizeDatabaseObjects(manifest.DatabaseObjects, backend)
	return err
}

func validateDatabaseObjects(objects []DatabaseObject) error {
	previous := ""
	for _, object := range objects {
		if err := validateStorageKey(object.StorageKey); err != nil || object.Size < 0 || !validSHA256(object.SHA256) {
			return fmt.Errorf("database object %q is invalid", object.StorageKey)
		}
		if previous != "" && object.StorageKey <= previous {
			return fmt.Errorf("database objects are not unique and sorted")
		}
		previous = object.StorageKey
	}
	return nil
}

func normalizeDatabaseObjects(objects []DatabaseObject, backend storage.Backend) ([]DatabaseObject, error) {
	if !backend.Valid() {
		return nil, fmt.Errorf("invalid storage backend %q", backend)
	}
	normalized := make([]DatabaseObject, len(objects))
	copy(normalized, objects)
	for index := range normalized {
		if strings.TrimSpace(normalized[index].Backend) == "" {
			normalized[index].Backend = string(storage.BackendLocal)
		}
		if normalized[index].Backend != string(backend) {
			return nil, fmt.Errorf("object %q belongs to backend %q, want %q", normalized[index].StorageKey, normalized[index].Backend, backend)
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].StorageKey < normalized[j].StorageKey })
	if err := validateDatabaseObjects(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func manifestStorageBackend(manifest Manifest) storage.Backend {
	if strings.TrimSpace(manifest.StorageBackend) == "" {
		return storage.BackendLocal
	}
	return storage.Backend(strings.TrimSpace(manifest.StorageBackend))
}

func matchDatabaseObjects(objects []DatabaseObject, files []FileArtifact, backend storage.Backend) error {
	byPath := make(map[string]FileArtifact, len(files))
	for _, file := range files {
		byPath[file.Path] = file
	}
	for _, object := range objects {
		artifact, ok := byPath[backupPathForObject(backend, object.StorageKey)]
		if !ok {
			return fmt.Errorf("database object %q is missing", object.StorageKey)
		}
		if artifact.Size != object.Size || artifact.SHA256 != object.SHA256 {
			return fmt.Errorf("database object %q does not match stored bytes", object.StorageKey)
		}
	}
	return nil
}

func backupPathForObject(backend storage.Backend, key string) string {
	if backend == storage.BackendS3 {
		return path.Join(storageDirectory, "objects", key)
	}
	return path.Join(storageDirectory, key)
}

func compareDatabaseObjects(expected, actual []DatabaseObject, backend storage.Backend) error {
	normalizedExpected, err := normalizeDatabaseObjects(expected, backend)
	if err != nil {
		return err
	}
	normalizedActual, err := normalizeDatabaseObjects(actual, backend)
	if err != nil {
		return err
	}
	if len(normalizedActual) != len(normalizedExpected) {
		return fmt.Errorf("object count is %d, want %d", len(normalizedActual), len(normalizedExpected))
	}
	for index := range normalizedExpected {
		if normalizedActual[index] != normalizedExpected[index] {
			return fmt.Errorf("object inventory differs at index %d", index)
		}
	}
	return nil
}

func restoreSnapshotObjects(
	ctx context.Context,
	store SnapshotStorage,
	manifest Manifest,
	stage string,
) ([]DatabaseObject, error) {
	backend := manifestStorageBackend(manifest)
	uploaded := make([]DatabaseObject, 0, len(manifest.DatabaseObjects))
	for _, object := range manifest.DatabaseObjects {
		relative := strings.TrimPrefix(backupPathForObject(backend, object.StorageKey), storageDirectory+"/")
		source, err := secureJoin(stage, relative)
		if err != nil {
			return uploaded, err
		}
		file, err := os.Open(source)
		if err != nil {
			return uploaded, fmt.Errorf("open staged %s object %q: %w", backend, object.StorageKey, err)
		}
		_, putErr := store.PutSnapshot(ctx, object.StorageKey, file, object.Size, object.SHA256)
		closeErr := file.Close()
		if putErr != nil {
			return uploaded, fmt.Errorf("restore %s object %q: %w", backend, object.StorageKey, putErr)
		}
		if closeErr != nil {
			return uploaded, fmt.Errorf("close staged %s object %q: %w", backend, object.StorageKey, closeErr)
		}
		uploaded = append(uploaded, object)
	}
	return uploaded, nil
}

func copyStorageTree(ctx context.Context, sourceRoot, destinationRoot string) ([]FileArtifact, error) {
	if err := os.Mkdir(destinationRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create backup object directory: %w", err)
	}
	artifacts := make([]FileArtifact, 0)
	err := filepath.WalkDir(sourceRoot, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceRoot, source)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("storage path %q is a symbolic link", filepath.ToSlash(relative))
		}
		destination := filepath.Join(destinationRoot, relative)
		if entry.IsDir() {
			return os.Mkdir(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("storage path %q is not a regular file", filepath.ToSlash(relative))
		}
		manifestPath := path.Join(storageDirectory, filepath.ToSlash(relative))
		artifact, err := copyRegularFile(ctx, source, destination, manifestPath)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("copy storage tree: %w", err)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

func copySnapshotObjects(
	ctx context.Context,
	store SnapshotStorage,
	objects []DatabaseObject,
	destinationRoot string,
) ([]FileArtifact, error) {
	if store == nil || store.Backend() != storage.BackendS3 {
		return nil, fmt.Errorf("S3 snapshot storage is required")
	}
	if err := os.Mkdir(destinationRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create backup object directory: %w", err)
	}
	artifacts := make([]FileArtifact, 0, len(objects))
	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateStorageKey(object.StorageKey); err != nil {
			return nil, err
		}
		source, err := store.Open(ctx, object.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("open S3 object %q: %w", object.StorageKey, err)
		}
		destination := filepath.Join(destinationRoot, "objects", filepath.FromSlash(object.StorageKey))
		artifact, copyErr := copySnapshotFile(ctx, source, destination, backupPathForObject(storage.BackendS3, object.StorageKey), object.Size, object.SHA256)
		closeErr := source.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("copy S3 object %q: %w", object.StorageKey, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close S3 object %q: %w", object.StorageKey, closeErr)
		}
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

func copySnapshotFile(
	ctx context.Context,
	source storage.File,
	destination string,
	manifestPath string,
	expectedSize int64,
	expectedSHA256 string,
) (FileArtifact, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return FileArtifact{}, err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return FileArtifact{}, err
	}
	digest := sha256.New()
	written, copyErr := io.CopyBuffer(
		io.MultiWriter(output, digest),
		&contextReader{ctx: ctx, reader: source},
		make([]byte, 128*1024),
	)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return FileArtifact{}, copyErr
	}
	if closeErr != nil {
		return FileArtifact{}, closeErr
	}
	actualHash := hex.EncodeToString(digest.Sum(nil))
	if written != expectedSize || actualHash != expectedSHA256 {
		return FileArtifact{}, fmt.Errorf("%w: snapshot bytes do not match manifest", storage.ErrChecksumMismatch)
	}
	return FileArtifact{Path: manifestPath, Size: written, SHA256: actualHash}, nil
}

func copyRegularFile(ctx context.Context, source, destination, manifestPath string) (FileArtifact, error) {
	before, err := os.Lstat(source)
	if err != nil {
		return FileArtifact{}, err
	}
	if !before.Mode().IsRegular() {
		return FileArtifact{}, fmt.Errorf("source is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return FileArtifact{}, err
	}
	input, err := os.Open(source)
	if err != nil {
		return FileArtifact{}, err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return FileArtifact{}, err
	}
	digest := sha256.New()
	written, copyErr := io.CopyBuffer(
		io.MultiWriter(output, digest),
		&contextReader{ctx: ctx, reader: input},
		make([]byte, 128*1024),
	)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return FileArtifact{}, copyErr
	}
	if closeErr != nil {
		return FileArtifact{}, closeErr
	}
	after, err := os.Lstat(source)
	if err != nil {
		return FileArtifact{}, err
	}
	if !after.Mode().IsRegular() || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) || written != before.Size() {
		return FileArtifact{}, fmt.Errorf("source changed while it was copied")
	}
	return FileArtifact{Path: manifestPath, Size: written, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func inspectRegularFile(filename, manifestPath string) (FileArtifact, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return FileArtifact{}, err
	}
	if !before.Mode().IsRegular() {
		return FileArtifact{}, fmt.Errorf("%q is not a regular file", manifestPath)
	}
	file, err := os.Open(filename)
	if err != nil {
		return FileArtifact{}, err
	}
	digest := sha256.New()
	written, copyErr := io.CopyBuffer(digest, file, make([]byte, 128*1024))
	closeErr := file.Close()
	if copyErr != nil {
		return FileArtifact{}, copyErr
	}
	if closeErr != nil {
		return FileArtifact{}, closeErr
	}
	after, err := os.Lstat(filename)
	if err != nil {
		return FileArtifact{}, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) ||
		written != before.Size() {
		return FileArtifact{}, fmt.Errorf("%q changed while it was hashed", manifestPath)
	}
	return FileArtifact{Path: manifestPath, Size: written, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func rejectUnexpectedFiles(root string, expected map[string]FileArtifact) error {
	return filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil || relative == "." {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup path %q is a symbolic link", relative)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("backup path %q is not a regular file", relative)
		}
		if _, ok := expected[relative]; !ok {
			return fmt.Errorf("backup contains unexpected file %q", relative)
		}
		return nil
	})
}

func existingRealDirectory(value, label string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%s must be a real directory", label)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s links: %w", label, err)
	}
	return filepath.Clean(resolved), nil
}

func newDirectoryPath(value string) (string, string, error) {
	if strings.TrimSpace(value) == "" {
		return "", "", fmt.Errorf("backup destination is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", "", fmt.Errorf("resolve backup destination: %w", err)
	}
	if filepath.Base(absolute) == "." || filepath.Base(absolute) == string(filepath.Separator) {
		return "", "", fmt.Errorf("backup destination must name a new directory")
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", "", fmt.Errorf("backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("inspect backup destination: %w", err)
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", "", fmt.Errorf("create backup parent directory: %w", err)
	}
	parent, err = existingRealDirectory(parent, "backup parent directory")
	if err != nil {
		return "", "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), parent, nil
}

func emptyTargetDirectory(value string) (target, parent string, existed bool, err error) {
	if strings.TrimSpace(value) == "" {
		return "", "", false, fmt.Errorf("target storage root is required")
	}
	target, err = filepath.Abs(value)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve target storage root: %w", err)
	}
	parent = filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", "", false, fmt.Errorf("create target storage parent: %w", err)
	}
	parent, err = existingRealDirectory(parent, "target storage parent")
	if err != nil {
		return "", "", false, err
	}
	target = filepath.Join(parent, filepath.Base(target))
	info, statErr := os.Lstat(target)
	if errors.Is(statErr, os.ErrNotExist) {
		return target, parent, false, nil
	}
	if statErr != nil {
		return "", "", false, fmt.Errorf("inspect target storage root: %w", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", false, fmt.Errorf("target storage root must be a real directory")
	}
	empty, err := directoryEmpty(target)
	if err != nil {
		return "", "", false, err
	}
	if !empty {
		return "", "", false, fmt.Errorf("target storage root is not empty")
	}
	return target, parent, true, nil
}

func directoryEmpty(directory string) (bool, error) {
	handle, err := os.Open(directory)
	if err != nil {
		return false, err
	}
	defer handle.Close()
	_, err = handle.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

func pathsOverlap(first, second string) bool {
	return pathWithin(first, second) || pathWithin(second, first)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

func secureJoin(root, relative string) (string, error) {
	if err := validateBackupPath(relative); err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(relative))
	if !pathWithin(root, joined) {
		return "", fmt.Errorf("backup path %q escapes its root", relative)
	}
	return joined, nil
}

func validateBackupPath(value string) error {
	if value == "" || strings.ContainsAny(value, "\\:\x00") || path.IsAbs(value) ||
		path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("backup path %q is not canonical", value)
	}
	return nil
}

func validateStorageKey(value string) error {
	return validateBackupPath(value)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func readBounded(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := &io.LimitedReader{R: file, N: limit + 1}
	value, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return value, nil
}

func writeJSONFile(filename string, value any) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeExclusiveFile(filename string, value []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncDirectory(directory string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode backup manifest trailer: %w", err)
	}
	return fmt.Errorf("backup manifest contains multiple JSON values")
}

func summarize(root string, manifest Manifest) Summary {
	total := manifest.Database.Size
	for _, artifact := range manifest.StorageFiles {
		total += artifact.Size
	}
	return Summary{
		Path: root, CreatedAt: manifest.CreatedAt, ServerVersion: manifest.ServerVersion,
		ContractVersion: manifest.ContractVersion, StorageFiles: len(manifest.StorageFiles),
		DatabaseObjects: len(manifest.DatabaseObjects), TotalBytes: total,
	}
}

func runExternalCommand(
	ctx context.Context,
	runner CommandRunner,
	command Command,
	secrets ...string,
) error {
	stderr := &boundedBuffer{limit: maxCommandErrorBytes}
	command.Stderr = stderr
	if err := runner.Run(ctx, command); err != nil {
		failure := sanitizeCommandText(err.Error(), secrets)
		detail := sanitizeCommandText(stderr.String(), secrets)
		if detail != "" {
			return fmt.Errorf("%s: %s", failure, detail)
		}
		return errors.New(failure)
	}
	return nil
}

func sanitizeCommandText(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "external command failed"
	}
	return value
}

func passwordFromEnvironment(environment []string) string {
	for _, value := range environment {
		if strings.HasPrefix(value, "PGPASSWORD=") {
			return strings.TrimPrefix(value, "PGPASSWORD=")
		}
	}
	return ""
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = buffer.buffer.Write(value)
	}
	return written, nil
}

func (buffer *boundedBuffer) String() string {
	return buffer.buffer.String()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
