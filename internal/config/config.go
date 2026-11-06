// Package config reads the service settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Config holds every setting the API needs.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// The defaults match docker-compose.yml, so `go run ./cmd/api` works with no extra setup.
const (
	defaultHTTPAddr        = ":8080"
	defaultDatabaseURL     = "postgres://cdp:cdp@localhost:5544/cdp?sslmode=disable"
	defaultLogLevel        = "info"
	defaultShutdownTimeout = "10s"
)

// Load reads the settings from the environment and fills in defaults.
// It returns an error when a value is set but cannot be parsed.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    getenv("HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL: getenv("DATABASE_URL", defaultDatabaseURL),
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(getenv("LOG_LEVEL", defaultLogLevel))); err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	timeout, err := time.ParseDuration(getenv("SHUTDOWN_TIMEOUT", defaultShutdownTimeout))
	if err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
	}
	cfg.ShutdownTimeout = timeout

	return cfg, nil
}

// getenv returns the variable's value, or fallback when it is unset or empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
