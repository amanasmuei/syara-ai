// Package middleware provides HTTP middleware for the API server.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimitConfig holds configuration for rate limiting.
type RateLimitConfig struct {
	// Per-endpoint limits
	ChatRequests        Limit
	Downloads           Limit
	Conversations       Limit
	Default             Limit
	EnableMetrics       bool
	GracefulDegradation bool // Continue without rate limiting if Redis is unavailable
}

// Limit defines rate limit parameters.
type Limit struct {
	Requests int           // Number of requests allowed
	Window   time.Duration // Time window for the limit
}

// DefaultRateLimitConfig returns default rate limit configuration.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		ChatRequests: Limit{
			Requests: 20,
			Window:   1 * time.Minute,
		},
		Downloads: Limit{
			Requests: 100,
			Window:   1 * time.Hour,
		},
		Conversations: Limit{
			Requests: 50,
			Window:   1 * time.Minute,
		},
		Default: Limit{
			Requests: 100,
			Window:   1 * time.Minute,
		},
		EnableMetrics:       true,
		GracefulDegradation: true,
	}
}

// RateLimitStore defines the interface for rate limit storage.
type RateLimitStore interface {
	// Increment increments the counter for a key and returns the new count.
	// If the key doesn't exist, it creates it with expiration.
	Increment(ctx context.Context, key string, window time.Duration) (int64, error)
	// GetCount returns the current count for a key.
	GetCount(ctx context.Context, key string) (int64, error)
	// IsHealthy returns whether the store is operational.
	IsHealthy() bool
}

// MemoryRateLimitStore implements RateLimitStore using in-memory storage.
// This is suitable for single-instance deployments.
type MemoryRateLimitStore struct {
	mu      sync.RWMutex
	entries map[string]*rateLimitEntry
}

type rateLimitEntry struct {
	count     int64
	expiresAt time.Time
}

// NewMemoryRateLimitStore creates a new in-memory rate limit store.
func NewMemoryRateLimitStore() *MemoryRateLimitStore {
	store := &MemoryRateLimitStore{
		entries: make(map[string]*rateLimitEntry),
	}
	// Start cleanup goroutine
	go store.cleanup()
	return store
}

// Increment increments the counter for a key.
func (s *MemoryRateLimitStore) Increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	entry, exists := s.entries[key]

	if !exists || now.After(entry.expiresAt) {
		// Create new entry
		s.entries[key] = &rateLimitEntry{
			count:     1,
			expiresAt: now.Add(window),
		}
		return 1, nil
	}

	// Increment existing entry
	entry.count++
	return entry.count, nil
}

// GetCount returns the current count for a key.
func (s *MemoryRateLimitStore) GetCount(ctx context.Context, key string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if entry, exists := s.entries[key]; exists && time.Now().Before(entry.expiresAt) {
		return entry.count, nil
	}
	return 0, nil
}

// IsHealthy returns whether the store is operational.
func (s *MemoryRateLimitStore) IsHealthy() bool {
	return true
}

// cleanup removes expired entries periodically.
func (s *MemoryRateLimitStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for key, entry := range s.entries {
			if now.After(entry.expiresAt) {
				delete(s.entries, key)
			}
		}
		s.mu.Unlock()
	}
}

// RedisRateLimitStore implements RateLimitStore using Redis.
// This is suitable for multi-instance deployments.
type RedisRateLimitStore struct {
	client  RedisClient
	prefix  string
	healthy bool
	logger  *slog.Logger
}

// RedisClient defines the interface for Redis operations needed by rate limiting.
type RedisClient interface {
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Ping(ctx context.Context) error
}

// NewRedisRateLimitStore creates a new Redis-based rate limit store.
func NewRedisRateLimitStore(client RedisClient, prefix string, logger *slog.Logger) *RedisRateLimitStore {
	store := &RedisRateLimitStore{
		client:  client,
		prefix:  prefix,
		healthy: true,
		logger:  logger,
	}

	// Test connection
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Ping(ctx); err != nil {
			store.logger.Warn("Redis connection failed for rate limiting", "error", err)
			store.healthy = false
		}
	} else {
		store.healthy = false
	}

	return store
}

// Increment increments the counter for a key.
func (s *RedisRateLimitStore) Increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	if !s.IsHealthy() {
		return 0, fmt.Errorf("redis not available")
	}

	fullKey := fmt.Sprintf("%s:%s", s.prefix, key)
	count, err := s.client.Incr(ctx, fullKey)
	if err != nil {
		return 0, fmt.Errorf("failed to increment rate limit counter: %w", err)
	}

	// Set expiration on first increment
	if count == 1 {
		if err := s.client.Expire(ctx, fullKey, window); err != nil {
			s.logger.Warn("failed to set rate limit expiration", "key", fullKey, "error", err)
		}
	}

	return count, nil
}

// GetCount returns the current count for a key.
func (s *RedisRateLimitStore) GetCount(ctx context.Context, key string) (int64, error) {
	if !s.IsHealthy() {
		return 0, fmt.Errorf("redis not available")
	}

	fullKey := fmt.Sprintf("%s:%s", s.prefix, key)
	val, err := s.client.Get(ctx, fullKey)
	if err != nil {
		return 0, nil // Key doesn't exist
	}

	var count int64
	if _, err := fmt.Sscanf(val, "%d", &count); err != nil {
		return 0, nil
	}
	return count, nil
}

// IsHealthy returns whether the store is operational.
func (s *RedisRateLimitStore) IsHealthy() bool {
	return s.healthy && s.client != nil
}

// RateLimiter provides rate limiting middleware.
type RateLimiter struct {
	store   RateLimitStore
	config  RateLimitConfig
	logger  *slog.Logger
	metrics *RateLimitMetrics
}

// RateLimitMetrics tracks rate limiting statistics.
type RateLimitMetrics struct {
	mu       sync.Mutex
	Allowed  map[string]uint64
	Rejected map[string]uint64
}

// NewRateLimiter creates a new RateLimiter instance.
func NewRateLimiter(store RateLimitStore, config RateLimitConfig, logger *slog.Logger) *RateLimiter {
	return &RateLimiter{
		store:  store,
		config: config,
		logger: logger.With("component", "rate_limiter"),
		metrics: &RateLimitMetrics{
			Allowed:  make(map[string]uint64),
			Rejected: make(map[string]uint64),
		},
	}
}

// Middleware returns a rate limiting middleware for a specific limit type.
func (rl *RateLimiter) Middleware(limitType string) func(next http.Handler) http.Handler {
	limit := rl.getLimit(limitType)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Get client identifier
			clientID := rl.getClientID(r)
			key := fmt.Sprintf("%s:%s", limitType, clientID)

			// Check rate limit
			if !rl.store.IsHealthy() {
				if rl.config.GracefulDegradation {
					// Continue without rate limiting
					next.ServeHTTP(w, r)
					return
				}
				http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
				return
			}

			count, err := rl.store.Increment(ctx, key, limit.Window)
			if err != nil {
				rl.logger.Error("rate limit check failed", "error", err, "key", key)
				if rl.config.GracefulDegradation {
					next.ServeHTTP(w, r)
					return
				}
				http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
				return
			}

			// Set rate limit headers
			remaining := limit.Requests - int(count)
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit.Requests))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", int(limit.Window.Seconds())))

			// Check if limit exceeded
			if count > int64(limit.Requests) {
				rl.recordMetric(limitType, false)
				rl.logger.Warn("rate limit exceeded",
					"client_id", clientID,
					"limit_type", limitType,
					"count", count,
					"limit", limit.Requests,
				)

				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(limit.Window.Seconds())))
				http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
				return
			}

			rl.recordMetric(limitType, true)
			next.ServeHTTP(w, r)
		})
	}
}

// getLimit returns the limit configuration for a limit type.
func (rl *RateLimiter) getLimit(limitType string) Limit {
	switch limitType {
	case "chat":
		return rl.config.ChatRequests
	case "download":
		return rl.config.Downloads
	case "conversation":
		return rl.config.Conversations
	default:
		return rl.config.Default
	}
}

// getClientID extracts a unique client identifier from the request.
func (rl *RateLimiter) getClientID(r *http.Request) string {
	// Try to get real IP from X-Forwarded-For or X-Real-IP headers
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to remote address
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// recordMetric records rate limit metrics.
func (rl *RateLimiter) recordMetric(limitType string, allowed bool) {
	if !rl.config.EnableMetrics {
		return
	}

	rl.metrics.mu.Lock()
	defer rl.metrics.mu.Unlock()

	if allowed {
		rl.metrics.Allowed[limitType]++
	} else {
		rl.metrics.Rejected[limitType]++
	}
}

// GetMetrics returns current rate limit metrics.
func (rl *RateLimiter) GetMetrics() map[string]interface{} {
	rl.metrics.mu.Lock()
	defer rl.metrics.mu.Unlock()

	metrics := make(map[string]interface{})
	for k, v := range rl.metrics.Allowed {
		metrics[fmt.Sprintf("%s_allowed", k)] = v
	}
	for k, v := range rl.metrics.Rejected {
		metrics[fmt.Sprintf("%s_rejected", k)] = v
	}
	return metrics
}
