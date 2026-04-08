// Package cmd provides the CLI entry point.
package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nlink-jp/webhook-relay/internal/backend"
	"github.com/nlink-jp/webhook-relay/internal/backend/gcs"
	"github.com/nlink-jp/webhook-relay/internal/config"
	"github.com/nlink-jp/webhook-relay/internal/server"
)

// Run is the main entry point for the webhook-relay server.
func Run(version string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize backends
	backends := make(map[string]backend.Backend)

	if cfg.GCSBucket != "" {
		gcsBackend, err := gcs.New(ctx, cfg.GCSProject, cfg.GCSBucket)
		if err != nil {
			return fmt.Errorf("gcs backend: %w", err)
		}
		defer gcsBackend.Close()
		backends["gcs"] = gcsBackend
		log.Printf("GCS backend enabled: bucket=%s", cfg.GCSBucket)
	}

	if len(backends) == 0 {
		return fmt.Errorf("no backends configured (set WEBHOOK_RELAY_GCS_BUCKET)")
	}

	// Create server
	srv := server.New(cfg, backends)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		log.Printf("webhook-relay %s listening on :%d", version, cfg.Port)
		log.Printf("Rate limit: %.1f rps, burst %d", cfg.RateLimitRPS, cfg.RateLimitBurst)
		log.Printf("Max request size: %d bytes", cfg.MaxRequestBytes)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for interrupt or error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received %s, shutting down gracefully...", sig)
	case err := <-errCh:
		return err
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	return httpServer.Shutdown(shutdownCtx)
}
