// Package api provides HTTP API handlers and utilities.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/alqutdigital/islamic-banking-agent/internal/api/handlers"
	"github.com/alqutdigital/islamic-banking-agent/internal/api/middleware"
)

// RouterConfig holds configuration for the API router.
type RouterConfig struct {
	// CORS settings
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int

	// Timeout settings
	RequestTimeout time.Duration

	// Rate limiting
	EnableRateLimiting bool
	RateLimitConfig    middleware.RateLimitConfig
}

// DefaultRouterConfig returns a default router configuration.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		AllowedOrigins:     []string{"*"},
		AllowedMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:     []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:     []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"},
		AllowCredentials:   false,
		MaxAge:             300,
		RequestTimeout:     30 * time.Second,
		EnableRateLimiting: true,
		RateLimitConfig:    middleware.DefaultRateLimitConfig(),
	}
}

// Dependencies holds all dependencies required by the API handlers.
type Dependencies struct {
	Logger          *slog.Logger
	DB              handlers.Database
	ObjectStorage   handlers.ObjectStorage
	ChatService     handlers.ChatService
	RateLimitStore  middleware.RateLimitStore
	WSHub           WSHub
}

// WSHub defines the interface for WebSocket hub operations.
type WSHub interface {
	HandleWebSocket(w http.ResponseWriter, r *http.Request)
}

// NewRouter creates and configures a new Chi router with all middleware and routes.
func NewRouter(deps Dependencies, config RouterConfig) *chi.Mux {
	r := chi.NewRouter()

	// Create logger instance for middleware
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// ============================
	// Global Middleware Stack
	// ============================

	// 1. Request ID - generates unique ID for each request
	r.Use(chimiddleware.RequestID)

	// 2. Real IP - extracts real client IP from proxy headers
	r.Use(chimiddleware.RealIP)

	// 3. Custom structured logging
	r.Use(middleware.Logger(logger))

	// 4. Panic recovery
	r.Use(middleware.Recoverer(logger))

	// 5. Request timeout
	r.Use(chimiddleware.Timeout(config.RequestTimeout))

	// 6. CORS handling
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   config.AllowedOrigins,
		AllowedMethods:   config.AllowedMethods,
		AllowedHeaders:   config.AllowedHeaders,
		ExposedHeaders:   config.ExposedHeaders,
		AllowCredentials: config.AllowCredentials,
		MaxAge:           config.MaxAge,
	}))

	// ============================
	// Rate Limiter Setup
	// ============================
	var rateLimiter *middleware.RateLimiter
	if config.EnableRateLimiting {
		store := deps.RateLimitStore
		if store == nil {
			// Fall back to in-memory store
			store = middleware.NewMemoryRateLimitStore()
		}
		rateLimiter = middleware.NewRateLimiter(store, config.RateLimitConfig, logger)
	}

	// ============================
	// Health Check Routes (no rate limiting)
	// ============================
	r.Get("/health", handlers.HealthCheck())
	r.Get("/ready", handlers.ReadyCheck(deps.DB, deps.ObjectStorage))

	// ============================
	// WebSocket Route
	// ============================
	if deps.WSHub != nil {
		r.Get("/ws", deps.WSHub.HandleWebSocket)
	}

	// ============================
	// API v1 Routes
	// ============================
	r.Route("/api/v1", func(r chi.Router) {
		// Chat endpoint with rate limiting
		r.Route("/chat", func(r chi.Router) {
			if rateLimiter != nil {
				r.Use(rateLimiter.Middleware("chat"))
			}
			r.Post("/", handlers.HandleChat(deps.ChatService, logger))
		})

		// Conversation management with rate limiting
		r.Route("/conversations", func(r chi.Router) {
			if rateLimiter != nil {
				r.Use(rateLimiter.Middleware("conversation"))
			}
			r.Get("/", handlers.ListConversations(deps.DB, logger))
			r.Post("/", handlers.CreateConversation(deps.DB, logger))
			r.Get("/{id}", handlers.GetConversation(deps.DB, logger))
			r.Put("/{id}", handlers.UpdateConversation(deps.DB, logger))
			r.Delete("/{id}", handlers.DeleteConversation(deps.DB, logger))
			r.Get("/{id}/messages", handlers.GetConversationMessages(deps.DB, logger))
		})

		// Document download with rate limiting
		r.Route("/documents", func(r chi.Router) {
			if rateLimiter != nil {
				r.Use(rateLimiter.Middleware("download"))
			}
			r.Get("/{id}", handlers.GetDocument(deps.DB, logger))
			r.Get("/{id}/download", handlers.HandleDownload(deps.DB, deps.ObjectStorage, logger))
		})
	})

	return r
}

// Server represents the HTTP server.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// ServerConfig holds configuration for the HTTP server.
type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// DefaultServerConfig returns default server configuration.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Host:            "",
		Port:            8080,
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}
}

// NewServer creates a new HTTP server.
func NewServer(handler http.Handler, config ServerConfig, logger *slog.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         formatAddr(config.Host, config.Port),
			Handler:      handler,
			ReadTimeout:  config.ReadTimeout,
			WriteTimeout: config.WriteTimeout,
			IdleTimeout:  config.IdleTimeout,
		},
		logger: logger,
	}
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	s.logger.Info("starting HTTP server", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down HTTP server")
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the server address.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// formatAddr formats host and port into an address string.
func formatAddr(host string, port int) string {
	if host == "" {
		return fmt.Sprintf(":%d", port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}
