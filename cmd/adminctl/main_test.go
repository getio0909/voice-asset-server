package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func TestCapabilitiesCommand(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"capabilities"}, strings.NewReader(""), &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var response product.Capabilities
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if response.ContractVersion != product.ContractVersion {
		t.Fatalf("ContractVersion = %q", response.ContractVersion)
	}
}

func TestVersionCommand(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"version"}, strings.NewReader(""), &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var info product.BuildInfo
	if err := json.Unmarshal(output.Bytes(), &info); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if info != product.CurrentBuildInfo() {
		t.Fatalf("build info = %#v, want %#v", info, product.CurrentBuildInfo())
	}
}

func TestCreateAdminReadsPasswordFromStdin(t *testing.T) {
	var received auth.OwnerInput
	createOwner := func(_ context.Context, input auth.OwnerInput) (auth.Principal, error) {
		received = input
		return auth.Principal{
			UserID: "user-1", WorkspaceID: "workspace-1", Role: "owner", Email: input.Email,
		}, nil
	}
	var output bytes.Buffer
	err := runCreateAdmin(
		[]string{"--email", "owner@example.com", "--workspace", "Primary", "--password-stdin"},
		strings.NewReader("correct horse battery staple\r\n"),
		&output,
		createOwner,
	)
	if err != nil {
		t.Fatalf("runCreateAdmin() error = %v", err)
	}
	if received.Password != "correct horse battery staple" {
		t.Fatalf("Password = %q, want stdin value without line ending", received.Password)
	}
	if strings.Contains(output.String(), received.Password) {
		t.Fatal("command output contains the password")
	}
}

func TestCreateAdminRequiresPasswordStdinFlag(t *testing.T) {
	err := runCreateAdmin(
		[]string{"--email", "owner@example.com"},
		strings.NewReader("password"),
		&bytes.Buffer{},
		func(context.Context, auth.OwnerInput) (auth.Principal, error) {
			t.Fatal("createOwner called")
			return auth.Principal{}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "--password-stdin") {
		t.Fatalf("runCreateAdmin() error = %v, want password-stdin requirement", err)
	}
}

func TestBackupAndRestoreRequireExplicitSafetyConfirmations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DATABASE_URL", "postgres://example.invalid/voiceasset")
	t.Setenv("VOICEASSET_STORAGE_PATH", root)

	err := run([]string{
		"backup", "--output", filepath.Join(root, "..", "backup"),
	}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "offline confirmation") {
		t.Fatalf("backup error = %v, want offline confirmation", err)
	}

	err = run([]string{
		"restore", "--backup", filepath.Join(root, "missing"), "--storage", filepath.Join(root, "restored"),
	}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "empty-target confirmation") {
		t.Fatalf("restore error = %v, want empty-target confirmation", err)
	}
}

func TestBackupCommandsRejectMissingPaths(t *testing.T) {
	for _, command := range []string{"backup", "backup-verify", "restore"} {
		err := run([]string{command}, strings.NewReader(""), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "usage: adminctl") {
			t.Fatalf("run(%q) error = %v, want usage", command, err)
		}
	}
}
