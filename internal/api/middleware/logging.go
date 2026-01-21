// Package middleware provides HTTP middleware for the API server.
package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	size        int
}

// WriteHeader captures the status code.
func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

// Write captures the response size and writes to the underlying ResponseWriter.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	return n, err
}

// Logger returns a middleware that logs HTTP requests with structured logging.
func Logger(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Get request ID from context (set by RequestID middleware)
			requestID := middleware.GetReqID(r.Context())

			// Wrap the response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			// Log incoming request
			logger.Info("request started",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)

			// Process request
			next.ServeHTTP(wrapped, r)

			// Log completed request
			duration := time.Since(start)
			logger.Info("request completed",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.status,
				"duration_ms", duration.Milliseconds(),
				"size", wrapped.size,
			)
		})
	}
}

// Recoverer returns a middleware that recovers from panics and logs them.
func Recoverer(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					requestID := middleware.GetReqID(r.Context())

					// Log the panic
					logger.Error("panic recovered",
						"request_id", requestID,
						"method", r.Method,
						"path", r.URL.Path,
						"panic", rvr,
					)

					// Return 500 error
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
