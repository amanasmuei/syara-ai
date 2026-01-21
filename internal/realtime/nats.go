// Package realtime provides real-time processing components for the application.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// Stream names for JetStream.
const (
	StreamDocuments = "DOCUMENTS"
	StreamSearches  = "SEARCHES"
	StreamUpdates   = "UPDATES"
)

// Subject patterns for event routing.
const (
	SubjectDocumentChanged   = "updates.document.changed"
	SubjectDocumentProcessed = "updates.document.processed"
	SubjectDocumentIndexed   = "updates.document.indexed"
	SubjectCacheInvalidate   = "updates.cache.invalidate"
	SubjectSearchQuery       = "searches.query"
	SubjectDocumentIngested  = "documents.ingested"
)

// NATSConfig holds NATS connection configuration.
type NATSConfig struct {
	URL            string
	ClusterID      string
	MaxReconnects  int
	ReconnectWait  time.Duration
	ConnectTimeout time.Duration
}

// DefaultNATSConfig returns a sensible default configuration.
func DefaultNATSConfig() NATSConfig {
	return NATSConfig{
		URL:            "nats://localhost:4222",
		ClusterID:      "islamic-banking",
		MaxReconnects:  -1, // Infinite reconnects
		ReconnectWait:  2 * time.Second,
		ConnectTimeout: 10 * time.Second,
	}
}

// NATSClient wraps NATS connection and JetStream context.
type NATSClient struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	config NATSConfig
	logger *slog.Logger
	mu     sync.RWMutex
	subs   []*nats.Subscription
}

// NewNATSClient creates a new NATS client with JetStream support.
func NewNATSClient(cfg NATSConfig, logger *slog.Logger) (*NATSClient, error) {
	if logger == nil {
		logger = slog.Default()
	}

	client := &NATSClient{
		config: cfg,
		logger: logger.With("component", "nats"),
		subs:   make([]*nats.Subscription, 0),
	}

	if err := client.connect(); err != nil {
		return nil, err
	}

	return client, nil
}

// connect establishes the NATS connection with retry logic.
func (c *NATSClient) connect() error {
	opts := []nats.Option{
		nats.Name(c.config.ClusterID),
		nats.MaxReconnects(c.config.MaxReconnects),
		nats.ReconnectWait(c.config.ReconnectWait),
		nats.Timeout(c.config.ConnectTimeout),
		nats.DisconnectErrHandler(func(conn *nats.Conn, err error) {
			if err != nil {
				c.logger.Warn("disconnected from NATS", "error", err)
			}
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			c.logger.Info("reconnected to NATS", "url", conn.ConnectedUrl())
		}),
		nats.ClosedHandler(func(conn *nats.Conn) {
			c.logger.Info("NATS connection closed")
		}),
		nats.ErrorHandler(func(conn *nats.Conn, sub *nats.Subscription, err error) {
			c.logger.Error("NATS error", "error", err, "subject", sub.Subject)
		}),
	}

	conn, err := nats.Connect(c.config.URL, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := conn.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.js = js
	c.mu.Unlock()

	c.logger.Info("connected to NATS", "url", c.config.URL)
	return nil
}

// SetupStreams creates the required JetStream streams.
func (c *NATSClient) SetupStreams(ctx context.Context) error {
	streams := []nats.StreamConfig{
		{
			Name:        StreamDocuments,
			Description: "Document ingestion events",
			Subjects:    []string{"documents.>"},
			Storage:     nats.FileStorage,
			Retention:   nats.LimitsPolicy,
			MaxAge:      7 * 24 * time.Hour, // Keep for 7 days
			MaxMsgs:     -1,
			MaxBytes:    -1,
			Replicas:    1,
			Discard:     nats.DiscardOld,
		},
		{
			Name:        StreamSearches,
			Description: "Search analytics events",
			Subjects:    []string{"searches.>"},
			Storage:     nats.FileStorage,
			Retention:   nats.LimitsPolicy,
			MaxAge:      30 * 24 * time.Hour, // Keep for 30 days
			MaxMsgs:     -1,
			MaxBytes:    -1,
			Replicas:    1,
			Discard:     nats.DiscardOld,
		},
		{
			Name:        StreamUpdates,
			Description: "Real-time update notifications",
			Subjects:    []string{"updates.>"},
			Storage:     nats.FileStorage,
			Retention:   nats.LimitsPolicy,
			MaxAge:      24 * time.Hour, // Keep for 24 hours
			MaxMsgs:     -1,
			MaxBytes:    -1,
			Replicas:    1,
			Discard:     nats.DiscardOld,
		},
	}

	c.mu.RLock()
	js := c.js
	c.mu.RUnlock()

	for _, cfg := range streams {
		// Check if stream exists
		_, err := js.StreamInfo(cfg.Name)
		if err != nil {
			if errors.Is(err, nats.ErrStreamNotFound) {
				// Create new stream
				_, err = js.AddStream(&cfg)
				if err != nil {
					return fmt.Errorf("failed to create stream %s: %w", cfg.Name, err)
				}
				c.logger.Info("created stream", "stream", cfg.Name)
			} else {
				return fmt.Errorf("failed to get stream info for %s: %w", cfg.Name, err)
			}
		} else {
			// Update existing stream
			_, err = js.UpdateStream(&cfg)
			if err != nil {
				c.logger.Warn("failed to update stream", "stream", cfg.Name, "error", err)
			} else {
				c.logger.Info("updated stream", "stream", cfg.Name)
			}
		}
	}

	return nil
}

// Publish publishes an event to a subject.
func (c *NATSClient) Publish(ctx context.Context, subject string, event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	c.mu.RLock()
	js := c.js
	c.mu.RUnlock()

	_, err = js.Publish(subject, data, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("failed to publish to %s: %w", subject, err)
	}

	c.logger.Debug("published event", "subject", subject, "size", len(data))
	return nil
}

// PublishAsync publishes an event asynchronously.
func (c *NATSClient) PublishAsync(subject string, event any) (nats.PubAckFuture, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}

	c.mu.RLock()
	js := c.js
	c.mu.RUnlock()

	future, err := js.PublishAsync(subject, data)
	if err != nil {
		return nil, fmt.Errorf("failed to publish async to %s: %w", subject, err)
	}

	return future, nil
}

// Subscribe creates a durable subscription to a subject.
func (c *NATSClient) Subscribe(
	subject string,
	handler func(*nats.Msg),
	opts ...nats.SubOpt,
) (*nats.Subscription, error) {
	c.mu.RLock()
	js := c.js
	c.mu.RUnlock()

	sub, err := js.Subscribe(subject, handler, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	c.mu.Lock()
	c.subs = append(c.subs, sub)
	c.mu.Unlock()

	c.logger.Info("subscribed to subject", "subject", subject)
	return sub, nil
}

// QueueSubscribe creates a queue subscription for load balancing.
func (c *NATSClient) QueueSubscribe(
	subject string,
	queue string,
	handler func(*nats.Msg),
	opts ...nats.SubOpt,
) (*nats.Subscription, error) {
	c.mu.RLock()
	js := c.js
	c.mu.RUnlock()

	sub, err := js.QueueSubscribe(subject, queue, handler, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to queue subscribe to %s: %w", subject, err)
	}

	c.mu.Lock()
	c.subs = append(c.subs, sub)
	c.mu.Unlock()

	c.logger.Info("queue subscribed to subject", "subject", subject, "queue", queue)
	return sub, nil
}

// JetStream returns the underlying JetStream context.
func (c *NATSClient) JetStream() nats.JetStreamContext {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.js
}

// Conn returns the underlying NATS connection.
func (c *NATSClient) Conn() *nats.Conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

// IsConnected returns true if connected to NATS.
func (c *NATSClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil && c.conn.IsConnected()
}

// Drain gracefully drains all subscriptions.
func (c *NATSClient) Drain() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, sub := range c.subs {
		if err := sub.Drain(); err != nil {
			c.logger.Warn("failed to drain subscription", "subject", sub.Subject, "error", err)
		}
	}

	if c.conn != nil {
		if err := c.conn.Drain(); err != nil {
			return fmt.Errorf("failed to drain connection: %w", err)
		}
	}

	c.logger.Info("drained all subscriptions")
	return nil
}

// Close closes the NATS connection.
func (c *NATSClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		c.js = nil
	}

	c.logger.Info("closed NATS connection")
	return nil
}

// Event types for the real-time system.

// DocumentChangedEvent is published when a source URL has new content.
type DocumentChangedEvent struct {
	EventID     string                 `json:"event_id"`
	SourceURL   string                 `json:"source_url"`
	SourceType  string                 `json:"source_type"` // "bnm", "aaoifi"
	Category    string                 `json:"category"`
	DetectedAt  time.Time              `json:"detected_at"`
	ContentHash string                 `json:"content_hash"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// NewDocumentChangedEvent creates a new DocumentChangedEvent with a generated ID.
func NewDocumentChangedEvent(sourceURL, sourceType, category, contentHash string) DocumentChangedEvent {
	return DocumentChangedEvent{
		EventID:     uuid.New().String(),
		SourceURL:   sourceURL,
		SourceType:  sourceType,
		Category:    category,
		DetectedAt:  time.Now().UTC(),
		ContentHash: contentHash,
		Metadata:    make(map[string]interface{}),
	}
}

// Validate checks if the event has required fields.
func (e *DocumentChangedEvent) Validate() error {
	if e.EventID == "" {
		return errors.New("event_id is required")
	}
	if e.SourceURL == "" {
		return errors.New("source_url is required")
	}
	if e.SourceType == "" {
		return errors.New("source_type is required")
	}
	return nil
}

// DocumentProcessedEvent is published when a document has been processed.
type DocumentProcessedEvent struct {
	EventID    string    `json:"event_id"`
	DocumentID string    `json:"document_id"`
	SourceURL  string    `json:"source_url"`
	SourceType string    `json:"source_type"`
	Title      string    `json:"title"`
	ChunkIDs   []string  `json:"chunk_ids"`
	ChunkCount int       `json:"chunk_count"`
	ProcessedAt time.Time `json:"processed_at"`
}

// NewDocumentProcessedEvent creates a new DocumentProcessedEvent.
func NewDocumentProcessedEvent(documentID, sourceURL, sourceType, title string, chunkIDs []string) DocumentProcessedEvent {
	return DocumentProcessedEvent{
		EventID:     uuid.New().String(),
		DocumentID:  documentID,
		SourceURL:   sourceURL,
		SourceType:  sourceType,
		Title:       title,
		ChunkIDs:    chunkIDs,
		ChunkCount:  len(chunkIDs),
		ProcessedAt: time.Now().UTC(),
	}
}

// Validate checks if the event has required fields.
func (e *DocumentProcessedEvent) Validate() error {
	if e.EventID == "" {
		return errors.New("event_id is required")
	}
	if e.DocumentID == "" {
		return errors.New("document_id is required")
	}
	return nil
}

// DocumentIndexedEvent is published when a document has been indexed in the vector store.
type DocumentIndexedEvent struct {
	EventID        string    `json:"event_id"`
	DocumentID     string    `json:"document_id"`
	SourceURL      string    `json:"source_url"`
	SourceType     string    `json:"source_type"`
	Title          string    `json:"title"`
	AffectedTopics []string  `json:"affected_topics"`
	IndexedAt      time.Time `json:"indexed_at"`
	CacheKeys      []string  `json:"cache_keys_to_invalidate"`
}

// NewDocumentIndexedEvent creates a new DocumentIndexedEvent.
func NewDocumentIndexedEvent(documentID, sourceURL, sourceType, title string, topics, cacheKeys []string) DocumentIndexedEvent {
	return DocumentIndexedEvent{
		EventID:        uuid.New().String(),
		DocumentID:     documentID,
		SourceURL:      sourceURL,
		SourceType:     sourceType,
		Title:          title,
		AffectedTopics: topics,
		IndexedAt:      time.Now().UTC(),
		CacheKeys:      cacheKeys,
	}
}

// Validate checks if the event has required fields.
func (e *DocumentIndexedEvent) Validate() error {
	if e.EventID == "" {
		return errors.New("event_id is required")
	}
	if e.DocumentID == "" {
		return errors.New("document_id is required")
	}
	return nil
}

// CacheInvalidateEvent is published when cache entries need to be invalidated.
type CacheInvalidateEvent struct {
	EventID    string    `json:"event_id"`
	CacheKeys  []string  `json:"cache_keys"`
	Patterns   []string  `json:"patterns"`
	Reason     string    `json:"reason"`
	TriggeredAt time.Time `json:"triggered_at"`
}

// NewCacheInvalidateEvent creates a new CacheInvalidateEvent.
func NewCacheInvalidateEvent(keys, patterns []string, reason string) CacheInvalidateEvent {
	return CacheInvalidateEvent{
		EventID:     uuid.New().String(),
		CacheKeys:   keys,
		Patterns:    patterns,
		Reason:      reason,
		TriggeredAt: time.Now().UTC(),
	}
}

// SearchQueryEvent is published when a search query is executed for analytics.
type SearchQueryEvent struct {
	EventID     string    `json:"event_id"`
	Query       string    `json:"query"`
	UserID      string    `json:"user_id,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	ResultCount int       `json:"result_count"`
	Latency     int64     `json:"latency_ms"`
	Timestamp   time.Time `json:"timestamp"`
}

// NewSearchQueryEvent creates a new SearchQueryEvent.
func NewSearchQueryEvent(query, userID, sessionID string, resultCount int, latencyMs int64) SearchQueryEvent {
	return SearchQueryEvent{
		EventID:     uuid.New().String(),
		Query:       query,
		UserID:      userID,
		SessionID:   sessionID,
		ResultCount: resultCount,
		Latency:     latencyMs,
		Timestamp:   time.Now().UTC(),
	}
}

// PublishDocumentChanged publishes a document changed event.
func (c *NATSClient) PublishDocumentChanged(ctx context.Context, event DocumentChangedEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}
	return c.Publish(ctx, SubjectDocumentChanged, event)
}

// PublishDocumentProcessed publishes a document processed event.
func (c *NATSClient) PublishDocumentProcessed(ctx context.Context, event DocumentProcessedEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}
	return c.Publish(ctx, SubjectDocumentProcessed, event)
}

// PublishDocumentIndexed publishes a document indexed event.
func (c *NATSClient) PublishDocumentIndexed(ctx context.Context, event DocumentIndexedEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}
	return c.Publish(ctx, SubjectDocumentIndexed, event)
}

// PublishCacheInvalidate publishes a cache invalidation event.
func (c *NATSClient) PublishCacheInvalidate(ctx context.Context, event CacheInvalidateEvent) error {
	return c.Publish(ctx, SubjectCacheInvalidate, event)
}

// PublishSearchQuery publishes a search query analytics event.
func (c *NATSClient) PublishSearchQuery(ctx context.Context, event SearchQueryEvent) error {
	return c.Publish(ctx, SubjectSearchQuery, event)
}
