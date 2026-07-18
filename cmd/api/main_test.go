package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/platform/config"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestVersionDoesNotRequireConfiguration(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"--version"}, &output); err != nil {
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

func TestSystemSettingConfigUsesOnlySafeRuntimeProjection(t *testing.T) {
	runtime := config.Config{
		BrandName:    "VoiceAsset Test",
		DatabaseURL:  "postgres://operator:password@database.example.test/voiceasset",
		PublicOrigin: "https://voice.example.test",
		CookieSecure: true,
		Storage: storage.Config{
			Backend:   storage.BackendS3,
			LocalRoot: "C:/private/storage",
			S3: storage.S3Config{
				Endpoint:        "https://objects.example.test",
				Bucket:          "private-bucket",
				AccessKeyID:     "not-for-output",
				SecretAccessKey: "not-for-output",
			},
		},
	}

	result := systemSettingConfig(runtime)
	if result.BrandName != runtime.BrandName || result.PublicOrigin != runtime.PublicOrigin ||
		result.StorageBackend != "s3" || !result.CookieSecure {
		t.Fatalf("projection = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"password", "private-bucket", "not-for-output", "C:/private/storage", "objects.example.test"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("projection contains %q: %s", forbidden, encoded)
		}
	}
}
