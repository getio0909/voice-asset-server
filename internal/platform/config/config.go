// Package config provides validated process configuration.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

const (
	defaultHTTPAddress    = ":8080"
	defaultShutdownPeriod = 10 * time.Second
)

// Config contains the API process configuration.
type Config struct {
	BrandName       string
	HTTPAddress     string
	ShutdownTimeout time.Duration
}

// Load reads environment variables and rejects invalid values.
func Load() (Config, error) {
	cfg := Config{
		BrandName:       envOrDefault("VOICEASSET_BRAND_NAME", product.Name),
		HTTPAddress:     envOrDefault("VOICEASSET_HTTP_ADDR", defaultHTTPAddress),
		ShutdownTimeout: defaultShutdownPeriod,
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

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
