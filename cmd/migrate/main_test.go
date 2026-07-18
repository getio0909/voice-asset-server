package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func TestVersionDoesNotLoadMigrations(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"--version", "--dir", filepath.Join(t.TempDir(), "missing")}, &output); err != nil {
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

func TestDryRunDoesNotRequireDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "000001_initial.up.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	var output bytes.Buffer

	err := run([]string{"-dry-run", "-dir", dir}, &output)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(output.String(), "000001 initial") {
		t.Fatalf("output = %q", output.String())
	}
}
