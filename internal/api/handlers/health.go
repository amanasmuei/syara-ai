// Package handlers provides HTTP request handlers for the API.
package handlers

import (
	"context"
	"net/http"
	"time"
)

// HealthStatus represents the health check response.
type HealthStatus struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
}

// ReadyStatus represents the readiness check response.
type ReadyStatus struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
	Timestamp  string            `json:"timestamp"`
}

// HealthChecker defines an interface for components that can report health.
type HealthChecker interface {
	Health(ctx context.Context) error
}

// HealthCheck returns a handler that reports basic service health.
// This endpoint should always return 200 OK if the service is running.
func HealthCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		RespondJSON(w, http.StatusOK, HealthStatus{
			Status:    "healthy",
			Service:   "islamic-banking-agent",
			Version:   "0.1.0",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// ReadyCheck returns a handler that checks if all dependencies are ready.
// This is used by orchestrators to determine if the service can receive traffic.
func ReadyCheck(db Database, storage ObjectStorage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		status := ReadyStatus{
			Status:     "ready",
			Components: make(map[string]string),
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		}

		allReady := true

		// Check database
		if db != nil {
			if err := db.Health(ctx); err != nil {
				status.Components["database"] = "unhealthy: " + err.Error()
				allReady = false
			} else {
				status.Components["database"] = "healthy"
			}
		} else {
			status.Components["database"] = "not configured"
		}

		// Check object storage
		if storage != nil {
			if err := storage.Health(ctx); err != nil {
				status.Components["object_storage"] = "unhealthy: " + err.Error()
				allReady = false
			} else {
				status.Components["object_storage"] = "healthy"
			}
		} else {
			status.Components["object_storage"] = "not configured"
		}

		if !allReady {
			status.Status = "not ready"
			RespondJSON(w, http.StatusServiceUnavailable, status)
			return
		}

		RespondJSON(w, http.StatusOK, status)
	}
}
