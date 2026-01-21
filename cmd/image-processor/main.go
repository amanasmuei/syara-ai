// Package main is the entry point for the visual processing service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/config"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/alqutdigital/islamic-banking-agent/pkg/shutdown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize logger
	log := logger.New(logger.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	})
	log.SetDefault()

	log.Info("starting image processor service",
		"version", "0.1.0",
		"environment", cfg.Server.Environment,
	)

	// Initialize shutdown handler
	shutdownHandler := shutdown.New(log.Logger, time.Duration(cfg.Server.ShutdownTimeout)*time.Second)

	// Initialize HTTP server for image processing API
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy","service":"image-processor"}`))
	})

	// TODO: Add image processing endpoints (ALQ-33, ALQ-34, ALQ-35, ALQ-36)
	// POST /api/v1/highlight - Highlight regions in PDF page
	// POST /api/v1/crop - Crop citation region
	// POST /api/v1/render - Render PDF page to image

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port+1), // Use port+1 for image processor
		Handler:      mux,
		ReadTimeout:  30 * time.Second, // Longer timeout for image processing
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Register server shutdown
	shutdownHandler.RegisterNamed("http-server", func(ctx context.Context) error {
		return server.Shutdown(ctx)
	})

	// Start server in goroutine
	go func() {
		log.Info("image processor server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
		}
	}()

	// Wait for shutdown signal
	shutdownHandler.Wait()

	log.Info("image processor stopped")
	return nil
}
