package backup

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPostgresCommandConnectionKeepsPasswordOutOfArguments(t *testing.T) {
	connection, environment, err := postgresCommandConnection(
		"postgres://backup-user:never-log-this@example.test:5544/voiceasset?sslmode=require",
	)
	if err != nil {
		t.Fatalf("postgresCommandConnection() error = %v", err)
	}
	for _, expected := range []string{
		"host='example.test'", "port='5544'", "dbname='voiceasset'",
		"user='backup-user'", "sslmode='require'",
	} {
		if !strings.Contains(connection, expected) {
			t.Fatalf("connection = %q, want %q", connection, expected)
		}
	}
	if strings.Contains(connection, "never-log-this") {
		t.Fatal("connection argument contains the database password")
	}
	if !slices.Contains(environment, "PGPASSWORD=never-log-this") {
		t.Fatalf("environment = %q, want password environment", environment)
	}
}

func TestPostgresCommandConnectionSupportsUnixSocketConninfo(t *testing.T) {
	connection, environment, err := postgresCommandConnection(
		"host=/var/run/postgresql user=voiceasset dbname=voiceasset_test",
	)
	if err != nil {
		t.Fatalf("postgresCommandConnection() error = %v", err)
	}
	for _, expected := range []string{
		"host='/var/run/postgresql'", "dbname='voiceasset_test'",
		"user='voiceasset'", "sslmode='disable'",
	} {
		if !strings.Contains(connection, expected) {
			t.Fatalf("connection = %q, want %q", connection, expected)
		}
	}
	if !slices.Contains(environment, "PGPASSWORD=") {
		t.Fatalf("environment = %q, want explicit empty password", environment)
	}
}

func TestExternalCommandErrorIsBoundedAndRedacted(t *testing.T) {
	runner := commandRunnerFunc(func(_ context.Context, command Command) error {
		_, _ = command.Stderr.Write([]byte("never-log-this " + strings.Repeat("diagnostic ", maxCommandErrorBytes)))
		return errors.New("exit status 1: never-log-this")
	})
	err := runExternalCommand(context.Background(), runner, Command{Name: "pg_dump"}, "never-log-this")
	if err == nil {
		t.Fatal("runExternalCommand() error = nil")
	}
	if len(err.Error()) > maxCommandErrorBytes+100 {
		t.Fatalf("error length = %d, want bounded diagnostic", len(err.Error()))
	}
	if strings.Contains(err.Error(), "never-log-this") {
		t.Fatal("command error contains a secret")
	}
}

type commandRunnerFunc func(context.Context, Command) error

func (run commandRunnerFunc) Run(ctx context.Context, command Command) error {
	return run(ctx, command)
}
