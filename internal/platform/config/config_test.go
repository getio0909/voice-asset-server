package config

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("VOICEASSET_BRAND_NAME", "")
	t.Setenv("VOICEASSET_HTTP_ADDR", "")
	t.Setenv("VOICEASSET_SHUTDOWN_TIMEOUT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("VOICEASSET_STORAGE_BACKEND", "")
	t.Setenv("VOICEASSET_STORAGE_PATH", "")
	clearS3Environment(t)
	t.Setenv("VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("VOICEASSET_PUBLIC_ORIGIN", "")
	t.Setenv("VOICEASSET_COOKIE_SECURE", "")
	t.Setenv("VOICEASSET_PROFILE_MASTER_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BrandName != "VoiceAsset" {
		t.Fatalf("BrandName = %q, want VoiceAsset", cfg.BrandName)
	}
	if cfg.HTTPAddress != ":8080" {
		t.Fatalf("HTTPAddress = %q, want :8080", cfg.HTTPAddress)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.DatabaseURL == "" || cfg.Storage.Backend != storage.BackendLocal || cfg.Storage.LocalRoot != "./var/objects" {
		t.Fatalf("unexpected storage configuration: %+v", cfg)
	}
	if cfg.PublicOrigin != "http://localhost:8080" || cfg.CookieSecure {
		t.Fatalf("unexpected local web defaults: %+v", cfg)
	}
}

func TestLoadAcceptsS3StorageBackend(t *testing.T) {
	clearS3Environment(t)
	t.Setenv("VOICEASSET_STORAGE_BACKEND", "S3")
	t.Setenv("VOICEASSET_S3_ENDPOINT", "https://objects.example.test")
	t.Setenv("VOICEASSET_S3_BUCKET", "voiceasset-test")
	t.Setenv("VOICEASSET_S3_PREFIX", "production/voiceasset")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Storage.Backend != storage.BackendS3 || !cfg.Storage.S3.ForcePathStyle ||
		cfg.Storage.S3.Region != "us-east-1" || cfg.Storage.S3.TempRoot != "./var/s3-temp" {
		t.Fatalf("Storage = %+v", cfg.Storage)
	}
}

func TestLoadRejectsUnknownStorageBackend(t *testing.T) {
	t.Setenv("VOICEASSET_STORAGE_BACKEND", "filesystem")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid storage backend error")
	}
}

func TestLoadRejectsInvalidS3Boolean(t *testing.T) {
	t.Setenv("VOICEASSET_STORAGE_BACKEND", "s3")
	t.Setenv("VOICEASSET_S3_FORCE_PATH_STYLE", "sometimes")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid S3 boolean error")
	}
}

func clearS3Environment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"VOICEASSET_S3_ENDPOINT", "VOICEASSET_S3_REGION", "VOICEASSET_S3_BUCKET",
		"VOICEASSET_S3_PREFIX", "VOICEASSET_S3_ACCESS_KEY_ID",
		"VOICEASSET_S3_SECRET_ACCESS_KEY", "VOICEASSET_S3_SESSION_TOKEN",
		"VOICEASSET_S3_FORCE_PATH_STYLE", "VOICEASSET_S3_CA_FILE", "VOICEASSET_S3_TEMP_PATH",
		"VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadAcceptsOptionalProfileMasterKey(t *testing.T) {
	t.Setenv("VOICEASSET_PROFILE_MASTER_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ProfileCipher == nil {
		t.Fatal("ProfileCipher = nil")
	}
}

func TestLoadRejectsInvalidProfileMasterKey(t *testing.T) {
	t.Setenv("VOICEASSET_PROFILE_MASTER_KEY", "not-a-valid-key")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid profile key error")
	}
}

func TestLoadRejectsInvalidCookieSecure(t *testing.T) {
	t.Setenv("VOICEASSET_COOKIE_SECURE", "sometimes")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid boolean error")
	}
}

func TestLoadDefaultsToSecureCookieForHTTPSOrigin(t *testing.T) {
	t.Setenv("VOICEASSET_PUBLIC_ORIGIN", "https://voice.example.com")
	t.Setenv("VOICEASSET_COOKIE_SECURE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.CookieSecure {
		t.Fatal("CookieSecure = false for HTTPS origin")
	}
}

func TestLoadRejectsInsecureCookieForHTTPSOrigin(t *testing.T) {
	t.Setenv("VOICEASSET_PUBLIC_ORIGIN", "https://voice.example.com")
	t.Setenv("VOICEASSET_COOKIE_SECURE", "false")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want insecure HTTPS cookie rejection")
	}
}

func TestLoadRejectsPublicOriginWithPath(t *testing.T) {
	t.Setenv("VOICEASSET_PUBLIC_ORIGIN", "https://voice.example.com/admin")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid origin error")
	}
}

func TestLoadRejectsInvalidShutdownTimeout(t *testing.T) {
	t.Setenv("VOICEASSET_SHUTDOWN_TIMEOUT", "never")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid duration error")
	}
}
