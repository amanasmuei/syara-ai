// Package storage provides Redis caching layer for RAG operations.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync/atomic"
	"time"
)

// RedisClient defines the interface for Redis operations.
// This allows for easy mocking in tests and flexibility with Redis client implementations.
type RedisClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Del(ctx context.Context, keys ...string) error
	Keys(ctx context.Context, pattern string) ([]string, error)
	Ping(ctx context.Context) error
	Close() error
}

// CacheConfig holds configuration for the cache manager.
type CacheConfig struct {
	Prefix               string
	EmbeddingTTL         time.Duration
	RetrievalTTL         time.Duration
	DocumentMetadataTTL  time.Duration
	MaxRetryAttempts     int
	RetryDelay           time.Duration
	EnableMetrics        bool
	GracefulDegradation  bool // Continue without cache if Redis is unavailable
}

// DefaultCacheConfig returns a default cache configuration.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		Prefix:               "rag",
		EmbeddingTTL:         1 * time.Hour,
		RetrievalTTL:         5 * time.Minute,
		DocumentMetadataTTL:  1 * time.Hour,
		MaxRetryAttempts:     3,
		RetryDelay:           100 * time.Millisecond,
		EnableMetrics:        true,
		GracefulDegradation:  true,
	}
}

// CacheMetrics tracks cache hit/miss statistics.
type CacheMetrics struct {
	EmbeddingHits   uint64
	EmbeddingMisses uint64
	RetrievalHits   uint64
	RetrievalMisses uint64
	DocumentHits    uint64
	DocumentMisses  uint64
	Errors          uint64
}

// CacheManager provides caching operations for RAG.
type CacheManager struct {
	client  RedisClient
	config  CacheConfig
	logger  *slog.Logger
	metrics *CacheMetrics
	healthy bool
}

// NewCacheManager creates a new CacheManager instance.
func NewCacheManager(client RedisClient, logger *slog.Logger, config CacheConfig) *CacheManager {
	if logger == nil {
		logger = slog.Default()
	}

	cm := &CacheManager{
		client:  client,
		config:  config,
		logger:  logger.With("component", "cache_manager"),
		metrics: &CacheMetrics{},
		healthy: true,
	}

	// Test connection
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Ping(ctx); err != nil {
			cm.logger.Warn("Redis connection failed, cache will be disabled", "error", err)
			cm.healthy = false
		}
	} else {
		cm.healthy = false
	}

	return cm
}

// IsHealthy returns whether the cache is operational.
func (cm *CacheManager) IsHealthy() bool {
	return cm.healthy && cm.client != nil
}

// GetMetrics returns current cache metrics.
func (cm *CacheManager) GetMetrics() CacheMetrics {
	return CacheMetrics{
		EmbeddingHits:   atomic.LoadUint64(&cm.metrics.EmbeddingHits),
		EmbeddingMisses: atomic.LoadUint64(&cm.metrics.EmbeddingMisses),
		RetrievalHits:   atomic.LoadUint64(&cm.metrics.RetrievalHits),
		RetrievalMisses: atomic.LoadUint64(&cm.metrics.RetrievalMisses),
		DocumentHits:    atomic.LoadUint64(&cm.metrics.DocumentHits),
		DocumentMisses:  atomic.LoadUint64(&cm.metrics.DocumentMisses),
		Errors:          atomic.LoadUint64(&cm.metrics.Errors),
	}
}

// GetEmbedding retrieves a cached embedding for a query.
func (cm *CacheManager) GetEmbedding(ctx context.Context, query string) ([]float32, bool, error) {
	if !cm.IsHealthy() {
		return nil, false, nil
	}

	key := cm.embeddingKey(query)
	start := time.Now()

	data, err := cm.client.Get(ctx, key)
	if err != nil {
		// Cache miss or error
		if cm.config.EnableMetrics {
			atomic.AddUint64(&cm.metrics.EmbeddingMisses, 1)
		}
		cm.logger.Debug("embedding cache miss",
			"query_hash", hashQuery(query),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return nil, false, nil
	}

	// Decode embedding
	embedding, err := decodeEmbedding([]byte(data))
	if err != nil {
		cm.logger.Error("failed to decode cached embedding", "error", err)
		atomic.AddUint64(&cm.metrics.Errors, 1)
		return nil, false, err
	}

	if cm.config.EnableMetrics {
		atomic.AddUint64(&cm.metrics.EmbeddingHits, 1)
	}

	cm.logger.Debug("embedding cache hit",
		"query_hash", hashQuery(query),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return embedding, true, nil
}

// SetEmbedding caches an embedding for a query.
func (cm *CacheManager) SetEmbedding(ctx context.Context, query string, embedding []float32) error {
	if !cm.IsHealthy() {
		return nil
	}

	key := cm.embeddingKey(query)
	data := encodeEmbedding(embedding)

	err := cm.client.Set(ctx, key, data, cm.config.EmbeddingTTL)
	if err != nil {
		cm.logger.Error("failed to cache embedding", "error", err)
		atomic.AddUint64(&cm.metrics.Errors, 1)
		if cm.config.GracefulDegradation {
			return nil
		}
		return err
	}

	cm.logger.Debug("embedding cached",
		"query_hash", hashQuery(query),
		"ttl", cm.config.EmbeddingTTL,
	)

	return nil
}

// GetRetrieval retrieves cached retrieval results.
func (cm *CacheManager) GetRetrieval(ctx context.Context, key string) ([]RetrievedChunk, bool, error) {
	if !cm.IsHealthy() {
		return nil, false, nil
	}

	cacheKey := cm.retrievalKey(key)
	start := time.Now()

	data, err := cm.client.Get(ctx, cacheKey)
	if err != nil {
		if cm.config.EnableMetrics {
			atomic.AddUint64(&cm.metrics.RetrievalMisses, 1)
		}
		cm.logger.Debug("retrieval cache miss",
			"key", key,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return nil, false, nil
	}

	var chunks []RetrievedChunk
	if err := json.Unmarshal([]byte(data), &chunks); err != nil {
		cm.logger.Error("failed to decode cached retrieval", "error", err)
		atomic.AddUint64(&cm.metrics.Errors, 1)
		return nil, false, err
	}

	if cm.config.EnableMetrics {
		atomic.AddUint64(&cm.metrics.RetrievalHits, 1)
	}

	cm.logger.Debug("retrieval cache hit",
		"key", key,
		"chunks", len(chunks),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return chunks, true, nil
}

// SetRetrieval caches retrieval results.
func (cm *CacheManager) SetRetrieval(ctx context.Context, key string, chunks []RetrievedChunk) error {
	if !cm.IsHealthy() {
		return nil
	}

	cacheKey := cm.retrievalKey(key)

	data, err := json.Marshal(chunks)
	if err != nil {
		cm.logger.Error("failed to encode retrieval for cache", "error", err)
		return err
	}

	err = cm.client.Set(ctx, cacheKey, data, cm.config.RetrievalTTL)
	if err != nil {
		cm.logger.Error("failed to cache retrieval", "error", err)
		atomic.AddUint64(&cm.metrics.Errors, 1)
		if cm.config.GracefulDegradation {
			return nil
		}
		return err
	}

	cm.logger.Debug("retrieval cached",
		"key", key,
		"chunks", len(chunks),
		"ttl", cm.config.RetrievalTTL,
	)

	return nil
}

// GetDocumentMetadata retrieves cached document metadata.
func (cm *CacheManager) GetDocumentMetadata(ctx context.Context, documentID string) (*Document, bool, error) {
	if !cm.IsHealthy() {
		return nil, false, nil
	}

	key := cm.documentKey(documentID)
	start := time.Now()

	data, err := cm.client.Get(ctx, key)
	if err != nil {
		if cm.config.EnableMetrics {
			atomic.AddUint64(&cm.metrics.DocumentMisses, 1)
		}
		cm.logger.Debug("document cache miss",
			"document_id", documentID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return nil, false, nil
	}

	var doc Document
	if err := json.Unmarshal([]byte(data), &doc); err != nil {
		cm.logger.Error("failed to decode cached document", "error", err)
		atomic.AddUint64(&cm.metrics.Errors, 1)
		return nil, false, err
	}

	if cm.config.EnableMetrics {
		atomic.AddUint64(&cm.metrics.DocumentHits, 1)
	}

	cm.logger.Debug("document cache hit",
		"document_id", documentID,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return &doc, true, nil
}

// SetDocumentMetadata caches document metadata.
func (cm *CacheManager) SetDocumentMetadata(ctx context.Context, doc *Document) error {
	if !cm.IsHealthy() || doc == nil {
		return nil
	}

	key := cm.documentKey(doc.ID.String())

	data, err := json.Marshal(doc)
	if err != nil {
		cm.logger.Error("failed to encode document for cache", "error", err)
		return err
	}

	err = cm.client.Set(ctx, key, data, cm.config.DocumentMetadataTTL)
	if err != nil {
		cm.logger.Error("failed to cache document", "error", err)
		atomic.AddUint64(&cm.metrics.Errors, 1)
		if cm.config.GracefulDegradation {
			return nil
		}
		return err
	}

	cm.logger.Debug("document cached",
		"document_id", doc.ID.String(),
		"ttl", cm.config.DocumentMetadataTTL,
	)

	return nil
}

// InvalidateByDocument invalidates all caches related to a document.
func (cm *CacheManager) InvalidateByDocument(ctx context.Context, documentID string) error {
	if !cm.IsHealthy() {
		return nil
	}

	// Delete document metadata cache
	docKey := cm.documentKey(documentID)
	if err := cm.client.Del(ctx, docKey); err != nil {
		cm.logger.Warn("failed to invalidate document cache", "document_id", documentID, "error", err)
	}

	// Invalidate retrieval caches (pattern-based)
	pattern := fmt.Sprintf("%s:retrieve:*", cm.config.Prefix)
	keys, err := cm.client.Keys(ctx, pattern)
	if err != nil {
		cm.logger.Warn("failed to get retrieval cache keys", "error", err)
		return nil
	}

	if len(keys) > 0 {
		if err := cm.client.Del(ctx, keys...); err != nil {
			cm.logger.Warn("failed to invalidate retrieval caches", "error", err)
		}
	}

	cm.logger.Info("invalidated caches for document", "document_id", documentID, "keys_deleted", len(keys)+1)
	return nil
}

// InvalidateBySource invalidates all caches for a source type.
func (cm *CacheManager) InvalidateBySource(ctx context.Context, sourceType string) error {
	if !cm.IsHealthy() {
		return nil
	}

	// Invalidate all retrieval caches (they may contain results from this source)
	pattern := fmt.Sprintf("%s:retrieve:*", cm.config.Prefix)
	keys, err := cm.client.Keys(ctx, pattern)
	if err != nil {
		cm.logger.Warn("failed to get retrieval cache keys", "error", err)
		return nil
	}

	if len(keys) > 0 {
		if err := cm.client.Del(ctx, keys...); err != nil {
			cm.logger.Warn("failed to invalidate retrieval caches", "error", err)
		}
	}

	cm.logger.Info("invalidated caches for source", "source_type", sourceType, "keys_deleted", len(keys))
	return nil
}

// InvalidateAll clears all RAG caches.
func (cm *CacheManager) InvalidateAll(ctx context.Context) error {
	if !cm.IsHealthy() {
		return nil
	}

	pattern := fmt.Sprintf("%s:*", cm.config.Prefix)
	keys, err := cm.client.Keys(ctx, pattern)
	if err != nil {
		cm.logger.Warn("failed to get cache keys", "error", err)
		return err
	}

	if len(keys) > 0 {
		if err := cm.client.Del(ctx, keys...); err != nil {
			cm.logger.Warn("failed to invalidate all caches", "error", err)
			return err
		}
	}

	cm.logger.Info("invalidated all caches", "keys_deleted", len(keys))
	return nil
}

// BuildRetrievalKey builds a cache key for retrieval results.
func (cm *CacheManager) BuildRetrievalKey(query string, sourceType string, topK int) string {
	return fmt.Sprintf("%s:%s:%d", hashQuery(query), sourceType, topK)
}

// Close closes the cache manager.
func (cm *CacheManager) Close() error {
	if cm.client != nil {
		return cm.client.Close()
	}
	return nil
}

// Key generation helpers

func (cm *CacheManager) embeddingKey(query string) string {
	return fmt.Sprintf("%s:embed:%s", cm.config.Prefix, hashQuery(query))
}

func (cm *CacheManager) retrievalKey(key string) string {
	return fmt.Sprintf("%s:retrieve:%s", cm.config.Prefix, key)
}

func (cm *CacheManager) documentKey(documentID string) string {
	return fmt.Sprintf("%s:doc:%s", cm.config.Prefix, documentID)
}

// hashQuery creates a hash of the query for use as a cache key.
func hashQuery(query string) string {
	h := sha256.Sum256([]byte(query))
	return hex.EncodeToString(h[:16]) // Use first 16 bytes for shorter key
}

// Embedding encoding/decoding helpers

// encodeEmbedding converts a float32 slice to bytes.
func encodeEmbedding(embedding []float32) []byte {
	buf := make([]byte, len(embedding)*4)
	for i, v := range embedding {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// decodeEmbedding converts bytes back to a float32 slice.
func decodeEmbedding(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid embedding data length: %d", len(data))
	}

	embedding := make([]float32, len(data)/4)
	for i := range embedding {
		embedding[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return embedding, nil
}

// NullCacheManager is a no-op cache manager for when caching is disabled.
type NullCacheManager struct{}

// NewNullCacheManager creates a no-op cache manager.
func NewNullCacheManager() *NullCacheManager {
	return &NullCacheManager{}
}

// GetEmbedding always returns a cache miss.
func (n *NullCacheManager) GetEmbedding(ctx context.Context, query string) ([]float32, bool, error) {
	return nil, false, nil
}

// SetEmbedding does nothing.
func (n *NullCacheManager) SetEmbedding(ctx context.Context, query string, embedding []float32) error {
	return nil
}

// GetRetrieval always returns a cache miss.
func (n *NullCacheManager) GetRetrieval(ctx context.Context, key string) ([]RetrievedChunk, bool, error) {
	return nil, false, nil
}

// SetRetrieval does nothing.
func (n *NullCacheManager) SetRetrieval(ctx context.Context, key string, chunks []RetrievedChunk) error {
	return nil
}
