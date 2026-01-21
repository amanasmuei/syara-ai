package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// Cache key prefixes.
const (
	CacheKeyPrefixEmbed    = "embed:"
	CacheKeyPrefixRetrieve = "retrieve:"
	CacheKeyPrefixDoc      = "doc:"
	CacheKeyPrefixQuery    = "query_cache:"
	CacheKeyPrefixSource   = "source_cache:"
)

// CacheInvalidatorConfig holds configuration for the cache invalidator.
type CacheInvalidatorConfig struct {
	BatchSize           int
	ScanCount           int64
	InvalidationTimeout time.Duration
	PublishChannel      string
	EnablePatternScan   bool
	MaxKeysPerPattern   int
}

// DefaultCacheInvalidatorConfig returns sensible defaults.
func DefaultCacheInvalidatorConfig() CacheInvalidatorConfig {
	return CacheInvalidatorConfig{
		BatchSize:           100,
		ScanCount:           1000,
		InvalidationTimeout: 5 * time.Second,
		PublishChannel:      "cache:invalidated",
		EnablePatternScan:   true,
		MaxKeysPerPattern:   10000,
	}
}

// CacheInvalidator manages cache invalidation across the system.
type CacheInvalidator struct {
	redis   *redis.Client
	nats    *NATSClient
	config  CacheInvalidatorConfig
	logger  *slog.Logger
	subs    []*nats.Subscription
	metrics *CacheMetrics
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// CacheMetrics holds metrics for cache invalidation.
type CacheMetrics struct {
	KeysInvalidated     atomic.Int64
	PatternsProcessed   atomic.Int64
	InvalidationEvents  atomic.Int64
	InvalidationErrors  atomic.Int64
	LastInvalidationAt  atomic.Value // time.Time
}

// NewCacheMetrics creates a new CacheMetrics instance.
func NewCacheMetrics() *CacheMetrics {
	m := &CacheMetrics{}
	m.LastInvalidationAt.Store(time.Time{})
	return m
}

// InvalidationResult represents the result of a cache invalidation operation.
type InvalidationResult struct {
	KeysDeleted     int64    `json:"keys_deleted"`
	PatternsMatched int      `json:"patterns_matched"`
	Topics          []string `json:"topics"`
	Duration        int64    `json:"duration_ms"`
	Errors          []string `json:"errors,omitempty"`
}

// NewCacheInvalidator creates a new cache invalidator.
func NewCacheInvalidator(
	redisClient *redis.Client,
	natsClient *NATSClient,
	cfg CacheInvalidatorConfig,
	logger *slog.Logger,
) *CacheInvalidator {
	if logger == nil {
		logger = slog.Default()
	}

	return &CacheInvalidator{
		redis:   redisClient,
		nats:    natsClient,
		config:  cfg,
		logger:  logger.With("component", "cache_invalidator"),
		subs:    make([]*nats.Subscription, 0),
		metrics: NewCacheMetrics(),
	}
}

// Start starts the cache invalidator service.
func (c *CacheInvalidator) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	c.logger.Info("starting cache invalidator")

	// Subscribe to document indexed events
	js := c.nats.JetStream()
	sub, err := js.Subscribe(
		SubjectDocumentIndexed,
		func(msg *nats.Msg) {
			c.handleDocumentIndexed(ctx, msg)
		},
		nats.Durable("cache-invalidator-indexed"),
		nats.ManualAck(),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe to document indexed: %w", err)
	}
	c.subs = append(c.subs, sub)

	// Subscribe to cache invalidation events (for manual invalidation requests)
	sub, err = js.Subscribe(
		SubjectCacheInvalidate,
		func(msg *nats.Msg) {
			c.handleCacheInvalidate(ctx, msg)
		},
		nats.Durable("cache-invalidator-manual"),
		nats.ManualAck(),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe to cache invalidate: %w", err)
	}
	c.subs = append(c.subs, sub)

	c.logger.Info("cache invalidator started")
	return nil
}

// Stop gracefully stops the cache invalidator.
func (c *CacheInvalidator) Stop(ctx context.Context) error {
	c.logger.Info("stopping cache invalidator")

	if c.cancel != nil {
		c.cancel()
	}

	// Drain subscriptions
	for _, sub := range c.subs {
		if err := sub.Drain(); err != nil {
			c.logger.Warn("failed to drain subscription", "error", err)
		}
	}

	// Wait for pending operations
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Info("cache invalidator stopped")
	case <-ctx.Done():
		c.logger.Warn("cache invalidator stop timed out")
		return ctx.Err()
	}

	return nil
}

// handleDocumentIndexed handles document indexed events.
func (c *CacheInvalidator) handleDocumentIndexed(ctx context.Context, msg *nats.Msg) {
	var event DocumentIndexedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		c.logger.Error("failed to unmarshal event", "error", err)
		msg.Nak()
		return
	}

	c.logger.Info("processing document indexed event for cache invalidation",
		"event_id", event.EventID,
		"document_id", event.DocumentID,
		"topics", event.AffectedTopics,
		"cache_keys", len(event.CacheKeys),
	)

	result, err := c.InvalidateOnUpdate(ctx, event)
	if err != nil {
		c.logger.Error("failed to invalidate cache",
			"event_id", event.EventID,
			"error", err,
		)
		msg.Nak()
		c.metrics.InvalidationErrors.Add(1)
		return
	}

	c.logger.Info("cache invalidation completed",
		"event_id", event.EventID,
		"keys_deleted", result.KeysDeleted,
		"patterns_matched", result.PatternsMatched,
		"duration_ms", result.Duration,
	)

	msg.Ack()
}

// handleCacheInvalidate handles manual cache invalidation events.
func (c *CacheInvalidator) handleCacheInvalidate(ctx context.Context, msg *nats.Msg) {
	var event CacheInvalidateEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		c.logger.Error("failed to unmarshal event", "error", err)
		msg.Nak()
		return
	}

	c.logger.Info("processing manual cache invalidation",
		"event_id", event.EventID,
		"keys", len(event.CacheKeys),
		"patterns", len(event.Patterns),
		"reason", event.Reason,
	)

	result, err := c.InvalidateByKeysAndPatterns(ctx, event.CacheKeys, event.Patterns)
	if err != nil {
		c.logger.Error("failed to invalidate cache",
			"event_id", event.EventID,
			"error", err,
		)
		msg.Nak()
		c.metrics.InvalidationErrors.Add(1)
		return
	}

	c.logger.Info("manual cache invalidation completed",
		"event_id", event.EventID,
		"keys_deleted", result.KeysDeleted,
		"duration_ms", result.Duration,
	)

	msg.Ack()
}

// InvalidateOnUpdate invalidates cache entries based on a document indexed event.
func (c *CacheInvalidator) InvalidateOnUpdate(ctx context.Context, event DocumentIndexedEvent) (*InvalidationResult, error) {
	start := time.Now()
	result := &InvalidationResult{
		Topics: event.AffectedTopics,
		Errors: make([]string, 0),
	}

	c.metrics.InvalidationEvents.Add(1)
	c.wg.Add(1)
	defer c.wg.Done()

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, c.config.InvalidationTimeout)
	defer cancel()

	// 1. Invalidate specific cache keys
	if len(event.CacheKeys) > 0 {
		deleted, err := c.deleteKeys(ctx, event.CacheKeys)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("delete keys: %v", err))
		}
		result.KeysDeleted += deleted
	}

	// 2. Invalidate document-specific cache
	docKey := CacheKeyPrefixDoc + event.DocumentID
	if err := c.redis.Del(ctx, docKey).Err(); err != nil && err != redis.Nil {
		result.Errors = append(result.Errors, fmt.Sprintf("delete doc key: %v", err))
	} else {
		result.KeysDeleted++
	}

	// 3. Invalidate by topic patterns
	if c.config.EnablePatternScan && len(event.AffectedTopics) > 0 {
		for _, topic := range event.AffectedTopics {
			patterns := []string{
				CacheKeyPrefixQuery + strings.ToLower(topic) + "*",
				CacheKeyPrefixRetrieve + "*" + strings.ToLower(topic) + "*",
			}

			for _, pattern := range patterns {
				deleted, err := c.invalidateByPattern(ctx, pattern)
				if err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("pattern %s: %v", pattern, err))
				}
				result.KeysDeleted += deleted
				result.PatternsMatched++
				c.metrics.PatternsProcessed.Add(1)
			}
		}
	}

	// 4. Invalidate source-specific cache
	if event.SourceType != "" {
		sourcePattern := CacheKeyPrefixSource + event.SourceType + "*"
		deleted, err := c.invalidateByPattern(ctx, sourcePattern)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("source pattern: %v", err))
		}
		result.KeysDeleted += deleted
		result.PatternsMatched++
	}

	// 5. Publish invalidation notification to Redis pub/sub
	if err := c.publishInvalidation(ctx, event); err != nil {
		c.logger.Warn("failed to publish invalidation notification", "error", err)
	}

	result.Duration = time.Since(start).Milliseconds()
	c.metrics.KeysInvalidated.Add(result.KeysDeleted)
	c.metrics.LastInvalidationAt.Store(time.Now())

	if len(result.Errors) > 0 {
		return result, fmt.Errorf("partial invalidation with %d errors", len(result.Errors))
	}

	return result, nil
}

// InvalidateByKeysAndPatterns invalidates specific keys and patterns.
func (c *CacheInvalidator) InvalidateByKeysAndPatterns(ctx context.Context, keys, patterns []string) (*InvalidationResult, error) {
	start := time.Now()
	result := &InvalidationResult{
		Errors: make([]string, 0),
	}

	c.metrics.InvalidationEvents.Add(1)
	c.wg.Add(1)
	defer c.wg.Done()

	ctx, cancel := context.WithTimeout(ctx, c.config.InvalidationTimeout)
	defer cancel()

	// Delete specific keys
	if len(keys) > 0 {
		deleted, err := c.deleteKeys(ctx, keys)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("delete keys: %v", err))
		}
		result.KeysDeleted += deleted
	}

	// Invalidate by patterns
	if c.config.EnablePatternScan {
		for _, pattern := range patterns {
			deleted, err := c.invalidateByPattern(ctx, pattern)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("pattern %s: %v", pattern, err))
			}
			result.KeysDeleted += deleted
			result.PatternsMatched++
			c.metrics.PatternsProcessed.Add(1)
		}
	}

	result.Duration = time.Since(start).Milliseconds()
	c.metrics.KeysInvalidated.Add(result.KeysDeleted)
	c.metrics.LastInvalidationAt.Store(time.Now())

	if len(result.Errors) > 0 {
		return result, fmt.Errorf("partial invalidation with %d errors", len(result.Errors))
	}

	return result, nil
}

// deleteKeys deletes specific keys from Redis.
func (c *CacheInvalidator) deleteKeys(ctx context.Context, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}

	// Delete in batches
	var totalDeleted int64
	for i := 0; i < len(keys); i += c.config.BatchSize {
		end := i + c.config.BatchSize
		if end > len(keys) {
			end = len(keys)
		}

		batch := keys[i:end]
		deleted, err := c.redis.Del(ctx, batch...).Result()
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to delete batch: %w", err)
		}
		totalDeleted += deleted
	}

	return totalDeleted, nil
}

// invalidateByPattern invalidates keys matching a pattern.
func (c *CacheInvalidator) invalidateByPattern(ctx context.Context, pattern string) (int64, error) {
	var totalDeleted int64
	var cursor uint64
	var keysProcessed int

	for {
		// Use SCAN to find keys matching pattern
		keys, nextCursor, err := c.redis.Scan(ctx, cursor, pattern, c.config.ScanCount).Result()
		if err != nil {
			return totalDeleted, fmt.Errorf("scan failed: %w", err)
		}

		if len(keys) > 0 {
			deleted, err := c.deleteKeys(ctx, keys)
			if err != nil {
				return totalDeleted, err
			}
			totalDeleted += deleted
			keysProcessed += len(keys)
		}

		// Check if we've hit the max keys limit
		if keysProcessed >= c.config.MaxKeysPerPattern {
			c.logger.Warn("max keys per pattern reached",
				"pattern", pattern,
				"keys_processed", keysProcessed,
			)
			break
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return totalDeleted, nil
}

// publishInvalidation publishes invalidation notification to Redis pub/sub.
func (c *CacheInvalidator) publishInvalidation(ctx context.Context, event DocumentIndexedEvent) error {
	notification := map[string]interface{}{
		"document_id":     event.DocumentID,
		"source_type":     event.SourceType,
		"affected_topics": event.AffectedTopics,
		"cache_keys":      event.CacheKeys,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	if err := c.redis.Publish(ctx, c.config.PublishChannel, data).Err(); err != nil {
		return fmt.Errorf("failed to publish to channel: %w", err)
	}

	return nil
}

// InvalidateDocument invalidates all cache entries for a specific document.
func (c *CacheInvalidator) InvalidateDocument(ctx context.Context, documentID string) error {
	patterns := []string{
		CacheKeyPrefixDoc + documentID,
		CacheKeyPrefixDoc + documentID + ":*",
		"*:" + documentID + ":*",
	}

	_, err := c.InvalidateByKeysAndPatterns(ctx, nil, patterns)
	return err
}

// InvalidateQuery invalidates cache entries for a specific query hash.
func (c *CacheInvalidator) InvalidateQuery(ctx context.Context, queryHash string) error {
	keys := []string{
		CacheKeyPrefixEmbed + queryHash,
		CacheKeyPrefixRetrieve + queryHash,
		CacheKeyPrefixQuery + queryHash,
	}

	_, err := c.deleteKeys(ctx, keys)
	return err
}

// InvalidateBySource invalidates all cache entries for a specific source.
func (c *CacheInvalidator) InvalidateBySource(ctx context.Context, sourceType string) error {
	pattern := CacheKeyPrefixSource + sourceType + "*"
	_, err := c.invalidateByPattern(ctx, pattern)
	return err
}

// InvalidateAll clears all cache entries (use with caution).
func (c *CacheInvalidator) InvalidateAll(ctx context.Context) error {
	c.logger.Warn("invalidating all cache entries")

	// Only invalidate our prefixed keys, not all Redis data
	prefixes := []string{
		CacheKeyPrefixEmbed,
		CacheKeyPrefixRetrieve,
		CacheKeyPrefixDoc,
		CacheKeyPrefixQuery,
		CacheKeyPrefixSource,
	}

	var totalDeleted int64
	for _, prefix := range prefixes {
		deleted, err := c.invalidateByPattern(ctx, prefix+"*")
		if err != nil {
			return fmt.Errorf("failed to invalidate prefix %s: %w", prefix, err)
		}
		totalDeleted += deleted
	}

	c.logger.Info("all cache entries invalidated", "keys_deleted", totalDeleted)
	return nil
}

// GetMetrics returns current cache invalidation metrics.
func (c *CacheInvalidator) GetMetrics() map[string]interface{} {
	lastInvalidation := c.metrics.LastInvalidationAt.Load().(time.Time)

	return map[string]interface{}{
		"keys_invalidated":     c.metrics.KeysInvalidated.Load(),
		"patterns_processed":   c.metrics.PatternsProcessed.Load(),
		"invalidation_events":  c.metrics.InvalidationEvents.Load(),
		"invalidation_errors":  c.metrics.InvalidationErrors.Load(),
		"last_invalidation_at": lastInvalidation,
	}
}

// Health checks if the cache invalidator is healthy.
func (c *CacheInvalidator) Health(ctx context.Context) error {
	// Check Redis connectivity
	if err := c.redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis not healthy: %w", err)
	}
	return nil
}
