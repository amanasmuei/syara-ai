// Package shutdown provides graceful shutdown handling.
package shutdown

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Handler manages graceful shutdown of multiple components.
type Handler struct {
	logger   *slog.Logger
	timeout  time.Duration
	cleanups []CleanupFunc
	mu       sync.Mutex
}

// CleanupFunc is a function called during shutdown.
type CleanupFunc func(ctx context.Context) error

// New creates a new shutdown handler.
func New(logger *slog.Logger, timeout time.Duration) *Handler {
	return &Handler{
		logger:   logger,
		timeout:  timeout,
		cleanups: make([]CleanupFunc, 0),
	}
}

// Register adds a cleanup function to be called during shutdown.
// Cleanup functions are called in LIFO order (last registered, first called).
func (h *Handler) Register(fn CleanupFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanups = append(h.cleanups, fn)
}

// RegisterNamed adds a named cleanup function for better logging.
func (h *Handler) RegisterNamed(name string, fn CleanupFunc) {
	h.Register(func(ctx context.Context) error {
		h.logger.Info("shutting down component", "component", name)
		if err := fn(ctx); err != nil {
			h.logger.Error("error shutting down component", "component", name, "error", err)
			return err
		}
		h.logger.Info("component shut down successfully", "component", name)
		return nil
	})
}

// Wait blocks until a shutdown signal is received, then performs cleanup.
func (h *Handler) Wait() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	sig := <-quit
	h.logger.Info("received shutdown signal", "signal", sig.String())

	h.Shutdown()
}

// Shutdown performs the actual shutdown process.
func (h *Handler) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	h.mu.Lock()
	cleanups := make([]CleanupFunc, len(h.cleanups))
	copy(cleanups, h.cleanups)
	h.mu.Unlock()

	// Execute cleanups in reverse order (LIFO)
	var wg sync.WaitGroup
	errors := make(chan error, len(cleanups))

	for i := len(cleanups) - 1; i >= 0; i-- {
		wg.Add(1)
		go func(fn CleanupFunc) {
			defer wg.Done()
			if err := fn(ctx); err != nil {
				errors <- err
			}
		}(cleanups[i])
	}

	// Wait for all cleanups or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		h.logger.Info("graceful shutdown completed")
	case <-ctx.Done():
		h.logger.Warn("shutdown timed out, forcing exit")
	}

	close(errors)
	for err := range errors {
		h.logger.Error("cleanup error", "error", err)
	}
}

// ListenAndShutdown is a convenience function that starts listening for signals
// in a goroutine and returns a channel that will be closed when shutdown is complete.
func (h *Handler) ListenAndShutdown() <-chan struct{} {
	done := make(chan struct{})

	go func() {
		h.Wait()
		close(done)
	}()

	return done
}
