package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults when nothing is set",
			env:  map[string]string{},
			want: Config{
				HTTPAddr:        ":8080",
				DatabaseURL:     defaultDatabaseURL,
				LogLevel:        slog.LevelInfo,
				ShutdownTimeout: 10 * time.Second,
			},
		},
		{
			name: "values from the environment",
			env: map[string]string{
				"HTTP_ADDR":        ":9090",
				"DATABASE_URL":     "postgres://u:p@db:5432/x",
				"LOG_LEVEL":        "debug",
				"SHUTDOWN_TIMEOUT": "3s",
			},
			want: Config{
				HTTPAddr:        ":9090",
				DatabaseURL:     "postgres://u:p@db:5432/x",
				LogLevel:        slog.LevelDebug,
				ShutdownTimeout: 3 * time.Second,
			},
		},
		{
			name:    "bad log level",
			env:     map[string]string{"LOG_LEVEL": "loud"},
			wantErr: true,
		},
		{
			name:    "bad shutdown timeout",
			env:     map[string]string{"SHUTDOWN_TIMEOUT": "ten seconds"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear every variable first so the developer's own shell does not leak into the test.
			for _, key := range []string{"HTTP_ADDR", "DATABASE_URL", "LOG_LEVEL", "SHUTDOWN_TIMEOUT"} {
				t.Setenv(key, "")
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
