// Package config provides validated process configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
	"github.com/getio0909/voice-asset-server/internal/platform/secretbox"
	"github.com/getio0909/voice-asset-server/internal/platform/telemetry"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	defaultHTTPAddress    = ":8080"
	defaultShutdownPeriod = 10 * time.Second
	defaultDatabaseURL    = "postgres://voiceasset:voiceasset-development-only@localhost:5432/voiceasset?sslmode=disable"
	defaultStorageBackend = "local"
	defaultStoragePath    = "./var/objects"
	defaultS3Region       = "us-east-1"
	defaultS3TempPath     = "./var/s3-temp"
	defaultPublicOrigin   = "http://localhost:8080"
	defaultFFmpegPath     = "ffmpeg"
)

// Config contains the API process configuration.
type Config struct {
	BrandName       string
	HTTPAddress     string
	ShutdownTimeout time.Duration
	DatabaseURL     string
	Storage         storage.Config
	PublicOrigin    string
	CookieSecure    bool
	ProfileCipher   *secretbox.Box
	FFmpegPath      string
	OTLPEndpoint    string
}

// Load reads environment variables and rejects invalid values.
func Load() (Config, error) {
	backend, err := storage.ParseBackend(envOrDefault("VOICEASSET_STORAGE_BACKEND", defaultStorageBackend))
	if err != nil {
		return Config{}, fmt.Errorf("parse VOICEASSET_STORAGE_BACKEND: %w", err)
	}
	s3Endpoint := strings.TrimSpace(os.Getenv("VOICEASSET_S3_ENDPOINT"))
	forcePathStyle := s3Endpoint != ""
	if backend == storage.BackendS3 {
		if value := strings.TrimSpace(os.Getenv("VOICEASSET_S3_FORCE_PATH_STYLE")); value != "" {
			forcePathStyle, err = strconv.ParseBool(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse VOICEASSET_S3_FORCE_PATH_STYLE: %w", err)
			}
		}
	}
	cfg := Config{
		BrandName:       envOrDefault("VOICEASSET_BRAND_NAME", product.Name),
		HTTPAddress:     envOrDefault("VOICEASSET_HTTP_ADDR", defaultHTTPAddress),
		ShutdownTimeout: defaultShutdownPeriod,
		DatabaseURL:     envOrDefault("DATABASE_URL", defaultDatabaseURL),
		Storage: storage.Config{
			Backend: backend, LocalRoot: envOrDefault("VOICEASSET_STORAGE_PATH", defaultStoragePath),
			S3: storage.S3Config{
				Endpoint: s3Endpoint, Region: envOrDefault("VOICEASSET_S3_REGION", defaultS3Region),
				Bucket:          strings.TrimSpace(os.Getenv("VOICEASSET_S3_BUCKET")),
				Prefix:          strings.TrimSpace(os.Getenv("VOICEASSET_S3_PREFIX")),
				AccessKeyID:     strings.TrimSpace(os.Getenv("VOICEASSET_S3_ACCESS_KEY_ID")),
				SecretAccessKey: strings.TrimSpace(os.Getenv("VOICEASSET_S3_SECRET_ACCESS_KEY")),
				SessionToken:    strings.TrimSpace(os.Getenv("VOICEASSET_S3_SESSION_TOKEN")),
				ForcePathStyle:  forcePathStyle,
				CAFile:          strings.TrimSpace(os.Getenv("VOICEASSET_S3_CA_FILE")),
				TempRoot:        envOrDefault("VOICEASSET_S3_TEMP_PATH", defaultS3TempPath),
			},
		},
		PublicOrigin: envOrDefault("VOICEASSET_PUBLIC_ORIGIN", defaultPublicOrigin),
		FFmpegPath:   envOrDefault("VOICEASSET_FFMPEG_PATH", defaultFFmpegPath),
		OTLPEndpoint: strings.TrimSpace(os.Getenv("VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT")),
	}

	if value := strings.TrimSpace(os.Getenv("VOICEASSET_SHUTDOWN_TIMEOUT")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse VOICEASSET_SHUTDOWN_TIMEOUT: %w", err)
		}
		if duration <= 0 {
			return Config{}, fmt.Errorf("VOICEASSET_SHUTDOWN_TIMEOUT must be positive")
		}
		cfg.ShutdownTimeout = duration
	}

	if strings.TrimSpace(cfg.BrandName) == "" {
		return Config{}, fmt.Errorf("VOICEASSET_BRAND_NAME must not be empty")
	}
	if strings.TrimSpace(cfg.HTTPAddress) == "" {
		return Config{}, fmt.Errorf("VOICEASSET_HTTP_ADDR must not be empty")
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must not be empty")
	}
	if err := cfg.Storage.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate storage configuration: %w", err)
	}
	if strings.TrimSpace(cfg.FFmpegPath) == "" {
		return Config{}, fmt.Errorf("VOICEASSET_FFMPEG_PATH must not be empty")
	}
	if err := telemetry.ValidateEndpoint(cfg.OTLPEndpoint); err != nil {
		return Config{}, fmt.Errorf("validate VOICEASSET_OTEL_EXPORTER_OTLP_ENDPOINT: %w", err)
	}
	origin, err := url.Parse(cfg.PublicOrigin)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return Config{}, fmt.Errorf("VOICEASSET_PUBLIC_ORIGIN must be an http(s) origin without a path")
	}
	cfg.CookieSecure = origin.Scheme == "https"
	if value := strings.TrimSpace(os.Getenv("VOICEASSET_COOKIE_SECURE")); value != "" {
		secure, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse VOICEASSET_COOKIE_SECURE: %w", err)
		}
		cfg.CookieSecure = secure
	}
	if origin.Scheme == "https" && !cfg.CookieSecure {
		return Config{}, fmt.Errorf("VOICEASSET_COOKIE_SECURE must be true for an https public origin")
	}
	if encodedKey := strings.TrimSpace(os.Getenv("VOICEASSET_PROFILE_MASTER_KEY")); encodedKey != "" {
		cipher, err := secretbox.New(encodedKey)
		if err != nil {
			return Config{}, fmt.Errorf("VOICEASSET_PROFILE_MASTER_KEY must be a base64-encoded 32-byte key")
		}
		cfg.ProfileCipher = cipher
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
