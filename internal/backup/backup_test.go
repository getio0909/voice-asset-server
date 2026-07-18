package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/storage"
)

const testDatabaseURL = "postgres://backup-user:never-log-this@example.test/voiceasset"

func TestCreateVerifyAndRestoreBackup(t *testing.T) {
	root := t.TempDir()
	storageRoot := filepath.Join(root, "source-objects")
	originalPath := filepath.Join(storageRoot, "objects", "aa", "original")
	partPath := filepath.Join(storageRoot, "parts", "bb", "1.part")
	writeTestFile(t, originalPath, []byte("immutable original audio"))
	writeTestFile(t, partPath, []byte("unfinished upload part"))
	originalBytes, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	objects := []DatabaseObject{{
		StorageKey: "objects/aa/original", Size: int64(len(originalBytes)), SHA256: digestBytes(originalBytes),
	}}
	database := &fakeDatabase{empty: true, objects: objects}
	runner := &fakeRunner{databaseURL: testDatabaseURL, dump: []byte("custom-format-database-archive")}
	destination := filepath.Join(root, "backups", "backup-001")
	created, err := Create(context.Background(), CreateOptions{
		DatabaseURL: testDatabaseURL, StorageRoot: storageRoot, Destination: destination,
		PGDumpPath: "pg_dump", ServerVersion: "1.2.3", ContractVersion: "0.7.0",
		ConfirmOffline: true, Runner: runner, Database: database,
		Now: func() time.Time { return time.Date(2026, 7, 16, 19, 40, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Path != destination || created.StorageFiles != 2 || created.DatabaseObjects != 1 {
		t.Fatalf("Create() summary = %+v", created)
	}
	if runner.dumpCalls != 1 || !runner.sawPasswordEnvironment || runner.sawSecretArgument {
		t.Fatalf("pg_dump calls/env/argument = %d/%t/%t", runner.dumpCalls, runner.sawPasswordEnvironment, runner.sawSecretArgument)
	}
	assertBackupContainsNoCredential(t, destination, testDatabaseURL)

	verified, err := Verify(context.Background(), VerifyOptions{
		BackupPath: destination, PGRestorePath: "pg_restore", Runner: runner,
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified != created || runner.listCalls != 1 {
		t.Fatalf("Verify() = %+v, list calls = %d", verified, runner.listCalls)
	}

	target := filepath.Join(root, "restored-objects")
	restored, err := Restore(context.Background(), RestoreOptions{
		BackupPath: destination, DatabaseURL: testDatabaseURL, StorageRoot: target,
		PGRestorePath: "pg_restore", ConfirmEmptyTarget: true, Runner: runner, Database: database,
	})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored.Path != target || runner.restoreCalls != 1 || runner.listCalls != 2 {
		t.Fatalf("Restore() = %+v, restore/list calls = %d/%d", restored, runner.restoreCalls, runner.listCalls)
	}
	for _, relative := range []string{"objects/aa/original", "parts/bb/1.part"} {
		want, err := os.ReadFile(filepath.Join(storageRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("restored %q = %q, want %q", relative, got, want)
		}
	}
	if runner.sawSecretArgument || !runner.sawRestoreDatabaseFlag {
		t.Fatalf("restore secret argument/flag = %t/%t", runner.sawSecretArgument, runner.sawRestoreDatabaseFlag)
	}
}

func TestCreateVerifyAndRestoreS3SnapshotBackup(t *testing.T) {
	root := t.TempDir()
	content := []byte("remote immutable original")
	part := []byte("remote unfinished part")
	objects := []DatabaseObject{
		{Backend: string(storage.BackendS3), StorageKey: "objects/aa/original", Size: int64(len(content)), SHA256: digestBytes(content)},
		{Backend: string(storage.BackendS3), StorageKey: "parts/bb/1.part", Size: int64(len(part)), SHA256: digestBytes(part)},
	}
	source := newFakeSnapshotStorage(content, part)
	database := &fakeDatabase{empty: true, objects: objects, inventory: objects}
	runner := &fakeRunner{databaseURL: testDatabaseURL, dump: []byte("s3-custom-format-database-archive")}
	destination := filepath.Join(root, "backups", "s3-backup-001")
	created, err := Create(context.Background(), CreateOptions{
		DatabaseURL: testDatabaseURL, Destination: destination,
		PGDumpPath: "pg_dump", ServerVersion: "1.2.3", ContractVersion: "0.7.0",
		ConfirmOffline: true, Runner: runner, Database: database, Storage: source,
	})
	if err != nil {
		t.Fatalf("Create(S3) error = %v", err)
	}
	if created.StorageFiles != 2 || created.DatabaseObjects != 2 {
		t.Fatalf("Create(S3) summary = %+v", created)
	}
	verified, err := Verify(context.Background(), VerifyOptions{BackupPath: destination, PGRestorePath: "pg_restore", Runner: runner})
	if err != nil || verified != created {
		t.Fatalf("Verify(S3) = %+v/%v, want %+v", verified, err, created)
	}
	target := newFakeSnapshotStorage()
	restored, err := Restore(context.Background(), RestoreOptions{
		BackupPath: destination, DatabaseURL: testDatabaseURL, StorageRoot: filepath.Join(root, "s3-staging"),
		PGRestorePath: "pg_restore", ConfirmEmptyTarget: true, Runner: runner,
		Database: database, TargetStorage: target,
	})
	if err != nil {
		t.Fatalf("Restore(S3) error = %v", err)
	}
	if restored.StorageFiles != 2 || target.values["objects/aa/original"] == nil || target.values["parts/bb/1.part"] == nil {
		t.Fatalf("Restore(S3) summary/objects = %+v/%v", restored, target.values)
	}
	failingTarget := newFakeSnapshotStorage()
	failingTarget.failKey = "parts/bb/1.part"
	_, err = Restore(context.Background(), RestoreOptions{
		BackupPath: destination, DatabaseURL: testDatabaseURL, StorageRoot: filepath.Join(root, "s3-failing-staging"),
		PGRestorePath: "pg_restore", ConfirmEmptyTarget: true, Runner: runner,
		Database: database, TargetStorage: failingTarget,
	})
	if err == nil || len(failingTarget.values) != 0 {
		t.Fatalf("Restore(S3) partial failure = %v, remaining objects = %v", err, failingTarget.values)
	}
}

func TestVerifyRejectsTamperedStorage(t *testing.T) {
	root, destination, runner := createTestBackup(t)
	_ = root
	artifact := filepath.Join(destination, storageDirectory, "objects", "aa", "original")
	if err := os.WriteFile(artifact, []byte("tampered bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(context.Background(), VerifyOptions{
		BackupPath: destination, PGRestorePath: "pg_restore", Runner: runner,
	})
	if err == nil || !strings.Contains(err.Error(), "manifest checksum") {
		t.Fatalf("Verify() error = %v, want file checksum failure", err)
	}
}

func TestCreateRejectsDatabaseObjectWithoutMatchingBytes(t *testing.T) {
	root := t.TempDir()
	storageRoot := filepath.Join(root, "objects")
	if err := os.Mkdir(storageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "backup")
	_, err := Create(context.Background(), CreateOptions{
		DatabaseURL: testDatabaseURL, StorageRoot: storageRoot, Destination: destination,
		ServerVersion: "test", ContractVersion: "0.7.0", ConfirmOffline: true,
		Runner: &fakeRunner{databaseURL: testDatabaseURL, dump: []byte("dump")},
		Database: &fakeDatabase{objects: []DatabaseObject{{
			StorageKey: "objects/missing/original", Size: 1, SHA256: digestBytes([]byte("x")),
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("Create() error = %v, want missing database object", err)
	}
	if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("backup destination was published after failure: %v", statErr)
	}
}

func TestRestoreRejectsNonEmptyTargets(t *testing.T) {
	root, destination, runner := createTestBackup(t)
	target := filepath.Join(root, "target")
	writeTestFile(t, filepath.Join(target, "existing"), []byte("do not replace"))
	_, err := Restore(context.Background(), RestoreOptions{
		BackupPath: destination, DatabaseURL: testDatabaseURL, StorageRoot: target,
		ConfirmEmptyTarget: true, Runner: runner, Database: &fakeDatabase{empty: true},
	})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("Restore() storage error = %v", err)
	}
	if runner.restoreCalls != 0 {
		t.Fatal("pg_restore ran for a non-empty storage target")
	}

	emptyTarget := filepath.Join(root, "empty-target")
	if err := os.Mkdir(emptyTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Restore(context.Background(), RestoreOptions{
		BackupPath: destination, DatabaseURL: testDatabaseURL, StorageRoot: emptyTarget,
		ConfirmEmptyTarget: true, Runner: runner, Database: &fakeDatabase{empty: false},
	})
	if err == nil || !strings.Contains(err.Error(), "clean database") {
		t.Fatalf("Restore() database error = %v", err)
	}
	if runner.restoreCalls != 0 {
		t.Fatal("pg_restore ran for a non-empty database target")
	}
}

func TestVerifyRejectsManifestPathTraversal(t *testing.T) {
	_, destination, runner := createTestBackup(t)
	manifestPath := filepath.Join(destination, manifestFilename)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.StorageFiles[0].Path = "objects/../../outside"
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	checksum := digestBytes(encoded) + "  " + manifestFilename + "\n"
	if err := os.WriteFile(filepath.Join(destination, manifestDigestName), []byte(checksum), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Verify(context.Background(), VerifyOptions{
		BackupPath: destination, PGRestorePath: "pg_restore", Runner: runner,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("Verify() error = %v, want invalid path", err)
	}
}

func TestVerifyRejectsSymlinkBeforeArchiveInspection(t *testing.T) {
	root, destination, runner := createTestBackup(t)
	external := filepath.Join(root, "external-object")
	writeTestFile(t, external, []byte("object bytes"))
	artifact := filepath.Join(destination, storageDirectory, "objects", "aa", "original")
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, artifact); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	_, err := Verify(context.Background(), VerifyOptions{
		BackupPath: destination, PGRestorePath: "pg_restore", Runner: runner,
	})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Verify() error = %v, want symbolic-link rejection", err)
	}
	if runner.listCalls != 0 {
		t.Fatal("pg_restore inspected an archive before a backup symlink was rejected")
	}
}

func createTestBackup(t *testing.T) (string, string, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	storageRoot := filepath.Join(root, "source")
	value := []byte("object bytes")
	writeTestFile(t, filepath.Join(storageRoot, "objects", "aa", "original"), value)
	runner := &fakeRunner{databaseURL: testDatabaseURL, dump: []byte("database archive")}
	destination := filepath.Join(root, "backup")
	_, err := Create(context.Background(), CreateOptions{
		DatabaseURL: testDatabaseURL, StorageRoot: storageRoot, Destination: destination,
		ServerVersion: "test", ContractVersion: "0.7.0", ConfirmOffline: true,
		Runner: runner, Database: &fakeDatabase{empty: true, objects: []DatabaseObject{{
			StorageKey: "objects/aa/original", Size: int64(len(value)), SHA256: digestBytes(value),
		}}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return root, destination, runner
}

type fakeRunner struct {
	databaseURL            string
	dump                   []byte
	dumpCalls              int
	listCalls              int
	restoreCalls           int
	sawPasswordEnvironment bool
	sawSecretArgument      bool
	sawRestoreDatabaseFlag bool
}

func (runner *fakeRunner) Run(_ context.Context, command Command) error {
	for _, argument := range command.Args {
		if strings.Contains(argument, runner.databaseURL) || strings.Contains(argument, "never-log-this") {
			runner.sawSecretArgument = true
		}
		if argument == "--dbname" {
			runner.sawRestoreDatabaseFlag = true
		}
	}
	if slices.Contains(command.Env, "PGPASSWORD=never-log-this") {
		runner.sawPasswordEnvironment = true
	}
	if slices.Contains(command.Args, "--format=custom") {
		runner.dumpCalls++
		index := slices.Index(command.Args, "--file")
		if index < 0 || index+1 >= len(command.Args) {
			return io.ErrUnexpectedEOF
		}
		return os.WriteFile(command.Args[index+1], runner.dump, 0o600)
	}
	if slices.Contains(command.Args, "--list") {
		runner.listCalls++
		return nil
	}
	runner.restoreCalls++
	return nil
}

type fakeDatabase struct {
	empty     bool
	objects   []DatabaseObject
	inventory []DatabaseObject
}

func (database *fakeDatabase) IsEmpty(context.Context, string) (bool, error) {
	return database.empty, nil
}

func (database *fakeDatabase) LoadLocalObjects(context.Context, string) ([]DatabaseObject, error) {
	return slices.Clone(database.objects), nil
}

func (database *fakeDatabase) LoadObjectInventory(context.Context, string, storage.Backend) ([]DatabaseObject, error) {
	if database.inventory == nil {
		return slices.Clone(database.objects), nil
	}
	return slices.Clone(database.inventory), nil
}

type fakeSnapshotStorage struct {
	values  map[string][]byte
	failKey string
}

func newFakeSnapshotStorage(values ...[]byte) *fakeSnapshotStorage {
	store := &fakeSnapshotStorage{values: make(map[string][]byte)}
	if len(values) > 0 {
		store.values["objects/aa/original"] = append([]byte(nil), values[0]...)
	}
	if len(values) > 1 {
		store.values["parts/bb/1.part"] = append([]byte(nil), values[1]...)
	}
	return store
}

func (*fakeSnapshotStorage) Backend() storage.Backend { return storage.BackendS3 }

func (store *fakeSnapshotStorage) Open(_ context.Context, key string) (storage.File, error) {
	value, ok := store.values[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	file, err := os.CreateTemp("", "voiceasset-backup-test-")
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return &cleanupFile{File: file}, nil
}

func (store *fakeSnapshotStorage) PutSnapshot(_ context.Context, key string, source io.Reader, expectedSize int64, expectedSHA256 string) (storage.Object, error) {
	value, err := io.ReadAll(source)
	if err != nil {
		return storage.Object{}, err
	}
	if int64(len(value)) != expectedSize || digestBytes(value) != expectedSHA256 {
		return storage.Object{}, storage.ErrChecksumMismatch
	}
	if key == store.failKey {
		return storage.Object{}, errors.New("injected snapshot failure")
	}
	if existing, ok := store.values[key]; ok {
		if !slices.Equal(existing, value) {
			return storage.Object{}, storage.ErrObjectConflict
		}
		return storage.Object{Backend: storage.BackendS3, Key: key, Size: expectedSize, SHA256: expectedSHA256, Reused: true}, nil
	}
	store.values[key] = append([]byte(nil), value...)
	return storage.Object{Backend: storage.BackendS3, Key: key, Size: expectedSize, SHA256: expectedSHA256}, nil
}

func (store *fakeSnapshotStorage) ListKeys(_ context.Context, _ string, limit int) ([]string, error) {
	keys := make([]string, 0, len(store.values))
	for key := range store.values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys, nil
}

func (store *fakeSnapshotStorage) DeleteObject(_ context.Context, key string, _ int64, _ string) error {
	delete(store.values, key)
	return nil
}

type cleanupFile struct{ *os.File }

func (file *cleanupFile) Close() error {
	name := file.Name()
	err := file.File.Close()
	_ = os.Remove(name)
	return err
}

func writeTestFile(t *testing.T, filename string, value []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, value, 0o600); err != nil {
		t.Fatal(err)
	}
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func assertBackupContainsNoCredential(t *testing.T, root, credential string) {
	t.Helper()
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		value, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if strings.Contains(string(value), credential) {
			t.Fatalf("backup file %q contains the database URL", filepath.Base(filename))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
