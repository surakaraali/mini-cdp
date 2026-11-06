// Command api starts the Mini CDP HTTP service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/surakaraali/mini-cdp/internal/config"
	"github.com/surakaraali/mini-cdp/internal/db"
	"github.com/surakaraali/mini-cdp/internal/httpapi"
	"github.com/surakaraali/mini-cdp/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped with an error", "error", err)
		os.Exit(1)
	}
}

// run holds the real startup logic. Returning errors (instead of calling os.Exit deep inside)
// lets the deferred cleanups, like closing the database pool, still run.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	// ctx is cancelled when the process receives Ctrl+C (SIGINT) or SIGTERM (sent by `docker stop`).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Running migrations at startup keeps local setup to one command.
	// With several API instances in production, this would be a separate deploy step instead.
	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	log.Info("migrations checked", "applied_now", applied)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewServer(log, httpapi.Deps{DB: pool, Events: store.New(pool)}).Handler(),
		ReadHeaderTimeout: 5 * time.Second, // protects against clients that send headers very slowly
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// ListenAndServe blocks until the server stops, so it runs in its own goroutine.
	// If it fails to start (for example, the port is taken), the error comes back on errCh.
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// Graceful shutdown: stop accepting new requests and give running ones time to finish.
	log.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
