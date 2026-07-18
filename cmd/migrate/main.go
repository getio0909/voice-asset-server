package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("voiceasset-migrate", flag.ContinueOnError)
	flags.SetOutput(output)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	directory := flags.String("dir", envOrDefault("MIGRATIONS_DIR", "migrations"), "migration directory")
	dryRun := flags.Bool("dry-run", false, "validate and list migrations without connecting")
	timeout := flags.Duration("timeout", 2*time.Minute, "overall migration timeout")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		return json.NewEncoder(output).Encode(product.CurrentBuildInfo())
	}

	files, err := migration.Load(*directory)
	if err != nil {
		return err
	}
	if *dryRun {
		for _, file := range files {
			fmt.Fprintf(output, "%06d %s %s\n", file.Version, file.Name, file.Checksum)
		}
		return nil
	}
	if *databaseURL == "" {
		return fmt.Errorf("database URL is required; set DATABASE_URL or use -database-url")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	connectionConfig, err := pgx.ParseConfig(*databaseURL)
	if err != nil {
		return fmt.Errorf("parse database URL")
	}
	connectionConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL")
	}
	defer conn.Close(context.Background())

	count, err := migration.Apply(ctx, conn, files)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "applied %d migration(s)\n", count)
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
