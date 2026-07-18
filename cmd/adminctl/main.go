package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/backup"
	"github.com/getio0909/voice-asset-server/internal/platform/config"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxPasswordInputBytes = 4096

type ownerCreator func(context.Context, auth.OwnerInput) (auth.Principal, error)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 {
		return adminUsageError()
	}
	switch args[0] {
	case "create-admin":
		return runCreateAdmin(args[1:], input, output, bootstrapOwnerFromEnvironment)
	case "backup":
		return runBackup(args[1:], output)
	case "backup-verify":
		return runBackupVerify(args[1:], output)
	case "restore":
		return runRestore(args[1:], output)
	}
	if len(args) != 1 {
		return adminUsageError()
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	switch args[0] {
	case "version":
		return encoder.Encode(product.CurrentBuildInfo())
	case "capabilities":
		return encoder.Encode(product.CurrentCapabilities())
	default:
		return fmt.Errorf("unknown command %q; %w", args[0], adminUsageError())
	}
}

func adminUsageError() error {
	return fmt.Errorf("usage: adminctl <version|capabilities|create-admin|backup|backup-verify|restore>")
}

func runCreateAdmin(args []string, input io.Reader, output io.Writer, createOwner ownerCreator) error {
	flags := flag.NewFlagSet("voiceasset-adminctl create-admin", flag.ContinueOnError)
	flags.SetOutput(output)
	email := flags.String("email", "", "initial owner email address")
	workspace := flags.String("workspace", "Primary Workspace", "initial workspace name")
	passwordStdin := flags.Bool("password-stdin", false, "read the owner password from stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*email) == "" {
		return fmt.Errorf("usage: adminctl create-admin --email <email> [--workspace <name>] --password-stdin")
	}
	if !*passwordStdin {
		return fmt.Errorf("--password-stdin is required; passwords are never accepted as command arguments")
	}
	password, err := readPassword(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	principal, err := createOwner(ctx, auth.OwnerInput{
		Email: *email, Password: password, WorkspaceName: *workspace,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(principal)
}

func readPassword(input io.Reader) (string, error) {
	value, err := io.ReadAll(io.LimitReader(input, maxPasswordInputBytes+1))
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(value) > maxPasswordInputBytes {
		return "", fmt.Errorf("password input exceeds %d bytes", maxPasswordInputBytes)
	}
	password := strings.TrimSuffix(string(value), "\n")
	password = strings.TrimSuffix(password, "\r")
	if strings.ContainsAny(password, "\r\n") {
		return "", fmt.Errorf("password input must contain exactly one line")
	}
	return password, nil
}

func runBackup(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("voiceasset-adminctl backup", flag.ContinueOnError)
	flags.SetOutput(output)
	destination := flags.String("output", "", "new backup directory")
	storageRoot := flags.String("storage", os.Getenv("VOICEASSET_STORAGE_PATH"), "offline local storage root or S3 restore staging root")
	pgDump := flags.String("pg-dump", "pg_dump", "pg_dump executable")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm API, worker, and Agent writes are stopped")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*destination) == "" {
		return fmt.Errorf("usage: adminctl backup --output <new-directory> --confirm-offline [--storage <path>]")
	}
	configured, snapshotStore, err := loadConfiguredStorage()
	if err != nil {
		return err
	}
	summary, err := backup.Create(context.Background(), backup.CreateOptions{
		DatabaseURL: configured.DatabaseURL, StorageRoot: *storageRoot, Destination: *destination,
		PGDumpPath: *pgDump, ServerVersion: product.ServerVersion, ContractVersion: product.ContractVersion,
		ConfirmOffline: *confirmOffline, Storage: snapshotStore,
	})
	if err != nil {
		return err
	}
	return writeJSON(output, summary)
}

func runBackupVerify(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("voiceasset-adminctl backup-verify", flag.ContinueOnError)
	flags.SetOutput(output)
	backupPath := flags.String("backup", "", "backup directory")
	pgRestore := flags.String("pg-restore", "pg_restore", "pg_restore executable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*backupPath) == "" {
		return fmt.Errorf("usage: adminctl backup-verify --backup <directory>")
	}
	summary, err := backup.Verify(context.Background(), backup.VerifyOptions{
		BackupPath: *backupPath, PGRestorePath: *pgRestore,
	})
	if err != nil {
		return err
	}
	return writeJSON(output, summary)
}

func runRestore(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("voiceasset-adminctl restore", flag.ContinueOnError)
	flags.SetOutput(output)
	backupPath := flags.String("backup", "", "verified backup directory")
	storageRoot := flags.String("storage", os.Getenv("VOICEASSET_STORAGE_PATH"), "new or empty local storage root or S3 staging root")
	pgRestore := flags.String("pg-restore", "pg_restore", "pg_restore executable")
	confirmEmpty := flags.Bool("confirm-empty-target", false, "confirm the database and storage target are disposable and empty")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*backupPath) == "" {
		return fmt.Errorf("usage: adminctl restore --backup <directory> --confirm-empty-target [--storage <path>]")
	}
	configured, snapshotStore, err := loadConfiguredStorage()
	if err != nil {
		return err
	}
	summary, err := backup.Restore(context.Background(), backup.RestoreOptions{
		BackupPath: *backupPath, DatabaseURL: configured.DatabaseURL, StorageRoot: *storageRoot,
		PGRestorePath: *pgRestore, ConfirmEmptyTarget: *confirmEmpty,
		TargetStorage: snapshotStore,
	})
	if err != nil {
		return err
	}
	return writeJSON(output, summary)
}

func loadConfiguredStorage() (config.Config, backup.SnapshotStorage, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("load configuration: %w", err)
	}
	if cfg.Storage.Backend == storage.BackendLocal {
		return cfg, nil, nil
	}
	driver, err := storage.New(cfg.Storage)
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("initialize storage: %w", err)
	}
	snapshotStore, ok := driver.(backup.SnapshotStorage)
	if !ok {
		return config.Config{}, nil, fmt.Errorf("storage driver does not support backup snapshots")
	}
	return cfg, snapshotStore, nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func bootstrapOwnerFromEnvironment(ctx context.Context, input auth.OwnerInput) (auth.Principal, error) {
	cfg, err := config.Load()
	if err != nil {
		return auth.Principal{}, fmt.Errorf("load configuration: %w", err)
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("connect to database")
	}
	defer pool.Close()
	service := auth.NewBootstrapService(auth.NewPostgresRepository(pool), auth.PasswordHasher{})
	return service.CreateOwner(ctx, input)
}
