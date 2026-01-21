package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// WorkerType defines the type of worker.
type WorkerType string

const (
	WorkerTypeCrawl   WorkerType = "crawl"
	WorkerTypeProcess WorkerType = "process"
	WorkerTypeIndex   WorkerType = "index"
)

// WorkerPoolConfig holds configuration for the worker pool.
type WorkerPoolConfig struct {
	CrawlWorkers   int
	ProcessWorkers int
	IndexWorkers   int
	MaxRetries     int
	RetryDelay     time.Duration
	AckWait        time.Duration
	MaxDeliver     int
}

// DefaultWorkerPoolConfig returns sensible defaults.
func DefaultWorkerPoolConfig() WorkerPoolConfig {
	return WorkerPoolConfig{
		CrawlWorkers:   3,
		ProcessWorkers: 2,
		IndexWorkers:   2,
		MaxRetries:     3,
		RetryDelay:     5 * time.Second,
		AckWait:        30 * time.Second,
		MaxDeliver:     4, // Original + 3 retries
	}
}

// DocumentProcessor is an interface for processing documents.
type DocumentProcessor interface {
	ProcessURL(ctx context.Context, url string) ([]ProcessedDocument, error)
}

// Embedder is an interface for generating embeddings.
type Embedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// VectorStore is an interface for storing document embeddings.
type VectorStore interface {
	Upsert(ctx context.Context, chunks []DocumentChunk) error
}

// ProcessedDocument represents a processed document.
type ProcessedDocument struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	SourceType  string    `json:"source_type"`
	Category    string    `json:"category"`
	ProcessedAt time.Time `json:"processed_at"`
}

// DocumentChunk represents a chunk of a document with its embedding.
type DocumentChunk struct {
	ID          string    `json:"id"`
	DocumentID  string    `json:"document_id"`
	Content     string    `json:"content"`
	Embedding   []float32 `json:"embedding,omitempty"`
	ChunkIndex  int       `json:"chunk_index"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// WorkerPool manages a pool of processing workers.
type WorkerPool struct {
	nats       *NATSClient
	config     WorkerPoolConfig
	logger     *slog.Logger
	processor  DocumentProcessor
	embedder   Embedder
	vectorDB   VectorStore
	workers    []*Worker
	subs       []*nats.Subscription
	wg         sync.WaitGroup
	cancel     context.CancelFunc
	metrics    *WorkerMetrics
}

// WorkerMetrics holds metrics for the worker pool.
type WorkerMetrics struct {
	JobsProcessed   atomic.Int64
	JobsFailed      atomic.Int64
	JobsRetried     atomic.Int64
	CurrentActive   atomic.Int64
	TotalLatencyMs  atomic.Int64
	LastProcessedAt atomic.Value // time.Time
}

// NewWorkerMetrics creates a new WorkerMetrics instance.
func NewWorkerMetrics() *WorkerMetrics {
	m := &WorkerMetrics{}
	m.LastProcessedAt.Store(time.Time{})
	return m
}

// Worker represents a single processing worker.
type Worker struct {
	ID         string
	Type       WorkerType
	pool       *WorkerPool
	logger     *slog.Logger
	processing atomic.Bool
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(
	natsClient *NATSClient,
	cfg WorkerPoolConfig,
	logger *slog.Logger,
	processor DocumentProcessor,
	embedder Embedder,
	vectorDB VectorStore,
) *WorkerPool {
	if logger == nil {
		logger = slog.Default()
	}

	return &WorkerPool{
		nats:      natsClient,
		config:    cfg,
		logger:    logger.With("component", "worker_pool"),
		processor: processor,
		embedder:  embedder,
		vectorDB:  vectorDB,
		workers:   make([]*Worker, 0),
		subs:      make([]*nats.Subscription, 0),
		metrics:   NewWorkerMetrics(),
	}
}

// Start starts all workers in the pool.
func (p *WorkerPool) Start(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)

	p.logger.Info("starting worker pool",
		"crawl_workers", p.config.CrawlWorkers,
		"process_workers", p.config.ProcessWorkers,
		"index_workers", p.config.IndexWorkers,
	)

	// Create consumer configuration
	consumerCfg := &nats.ConsumerConfig{
		Durable:       "processing-workers",
		DeliverPolicy: nats.DeliverAllPolicy,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       p.config.AckWait,
		MaxDeliver:    p.config.MaxDeliver,
		MaxAckPending: 100,
	}

	// Create or update consumer
	js := p.nats.JetStream()
	_, err := js.AddConsumer(StreamUpdates, consumerCfg)
	if err != nil {
		// Try updating if it exists
		_, err = js.UpdateConsumer(StreamUpdates, consumerCfg)
		if err != nil {
			p.logger.Warn("failed to create/update consumer", "error", err)
		}
	}

	// Start crawl workers
	for i := 0; i < p.config.CrawlWorkers; i++ {
		worker := p.createWorker(WorkerTypeCrawl, i)
		p.workers = append(p.workers, worker)
		p.wg.Add(1)
		go p.runCrawlWorker(ctx, worker)
	}

	// Start process workers (subscribed to document processed events)
	for i := 0; i < p.config.ProcessWorkers; i++ {
		worker := p.createWorker(WorkerTypeProcess, i)
		p.workers = append(p.workers, worker)
		p.wg.Add(1)
		go p.runProcessWorker(ctx, worker)
	}

	// Start index workers (subscribed to document indexed events for downstream)
	for i := 0; i < p.config.IndexWorkers; i++ {
		worker := p.createWorker(WorkerTypeIndex, i)
		p.workers = append(p.workers, worker)
		p.wg.Add(1)
		go p.runIndexWorker(ctx, worker)
	}

	p.logger.Info("worker pool started",
		"total_workers", len(p.workers),
	)

	return nil
}

// Stop gracefully stops all workers.
func (p *WorkerPool) Stop(ctx context.Context) error {
	p.logger.Info("stopping worker pool")

	if p.cancel != nil {
		p.cancel()
	}

	// Drain all subscriptions
	for _, sub := range p.subs {
		if err := sub.Drain(); err != nil {
			p.logger.Warn("failed to drain subscription",
				"subject", sub.Subject,
				"error", err,
			)
		}
	}

	// Wait for all workers to finish
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("worker pool stopped gracefully")
	case <-ctx.Done():
		p.logger.Warn("worker pool stop timed out")
		return ctx.Err()
	}

	return nil
}

// createWorker creates a new worker instance.
func (p *WorkerPool) createWorker(workerType WorkerType, index int) *Worker {
	return &Worker{
		ID:     fmt.Sprintf("%s-%d-%s", workerType, index, uuid.New().String()[:8]),
		Type:   workerType,
		pool:   p,
		logger: p.logger.With("worker_id", fmt.Sprintf("%s-%d", workerType, index)),
	}
}

// runCrawlWorker runs a crawl worker that processes document changed events.
func (p *WorkerPool) runCrawlWorker(ctx context.Context, worker *Worker) {
	defer p.wg.Done()

	worker.logger.Info("starting crawl worker")

	js := p.nats.JetStream()
	sub, err := js.QueueSubscribe(
		SubjectDocumentChanged,
		"crawl-workers",
		func(msg *nats.Msg) {
			p.handleDocumentChanged(ctx, worker, msg)
		},
		nats.Durable("crawl-worker"),
		nats.ManualAck(),
		nats.AckWait(p.config.AckWait),
		nats.MaxDeliver(p.config.MaxDeliver),
	)
	if err != nil {
		worker.logger.Error("failed to subscribe", "error", err)
		return
	}

	p.subs = append(p.subs, sub)

	<-ctx.Done()
	worker.logger.Info("crawl worker stopped")
}

// runProcessWorker runs a process worker that handles document processing.
func (p *WorkerPool) runProcessWorker(ctx context.Context, worker *Worker) {
	defer p.wg.Done()

	worker.logger.Info("starting process worker")

	js := p.nats.JetStream()
	sub, err := js.QueueSubscribe(
		SubjectDocumentProcessed,
		"process-workers",
		func(msg *nats.Msg) {
			p.handleDocumentProcessed(ctx, worker, msg)
		},
		nats.Durable("process-worker"),
		nats.ManualAck(),
		nats.AckWait(p.config.AckWait),
		nats.MaxDeliver(p.config.MaxDeliver),
	)
	if err != nil {
		worker.logger.Error("failed to subscribe", "error", err)
		return
	}

	p.subs = append(p.subs, sub)

	<-ctx.Done()
	worker.logger.Info("process worker stopped")
}

// runIndexWorker runs an index worker that handles document indexing completion.
func (p *WorkerPool) runIndexWorker(ctx context.Context, worker *Worker) {
	defer p.wg.Done()

	worker.logger.Info("starting index worker")

	js := p.nats.JetStream()
	sub, err := js.QueueSubscribe(
		SubjectDocumentIndexed,
		"index-workers",
		func(msg *nats.Msg) {
			p.handleDocumentIndexed(ctx, worker, msg)
		},
		nats.Durable("index-worker"),
		nats.ManualAck(),
		nats.AckWait(p.config.AckWait),
		nats.MaxDeliver(p.config.MaxDeliver),
	)
	if err != nil {
		worker.logger.Error("failed to subscribe", "error", err)
		return
	}

	p.subs = append(p.subs, sub)

	<-ctx.Done()
	worker.logger.Info("index worker stopped")
}

// handleDocumentChanged handles document changed events.
func (p *WorkerPool) handleDocumentChanged(ctx context.Context, worker *Worker, msg *nats.Msg) {
	start := time.Now()
	worker.processing.Store(true)
	p.metrics.CurrentActive.Add(1)
	defer func() {
		worker.processing.Store(false)
		p.metrics.CurrentActive.Add(-1)
		p.metrics.TotalLatencyMs.Add(time.Since(start).Milliseconds())
	}()

	var event DocumentChangedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		worker.logger.Error("failed to unmarshal event", "error", err)
		msg.Nak()
		p.metrics.JobsFailed.Add(1)
		return
	}

	worker.logger.Info("processing document changed event",
		"event_id", event.EventID,
		"source_url", event.SourceURL,
		"source_type", event.SourceType,
	)

	// Check if we have a processor configured
	if p.processor == nil {
		worker.logger.Debug("no processor configured, skipping processing")
		msg.Ack()
		p.metrics.JobsProcessed.Add(1)
		p.metrics.LastProcessedAt.Store(time.Now())
		return
	}

	// Process the URL
	documents, err := p.processor.ProcessURL(ctx, event.SourceURL)
	if err != nil {
		worker.logger.Error("failed to process URL",
			"url", event.SourceURL,
			"error", err,
		)

		// Check if we should retry
		metadata, _ := msg.Metadata()
		if metadata != nil && int(metadata.NumDelivered) >= p.config.MaxDeliver {
			worker.logger.Warn("max retries exceeded, moving to DLQ",
				"event_id", event.EventID,
				"deliveries", metadata.NumDelivered,
			)
			msg.Term()
			p.metrics.JobsFailed.Add(1)
		} else {
			msg.Nak()
			p.metrics.JobsRetried.Add(1)
		}
		return
	}

	// Publish processed events for each document
	for _, doc := range documents {
		processedEvent := NewDocumentProcessedEvent(
			doc.ID,
			doc.URL,
			doc.SourceType,
			doc.Title,
			[]string{}, // Chunk IDs will be added by process worker
		)

		if err := p.nats.PublishDocumentProcessed(ctx, processedEvent); err != nil {
			worker.logger.Error("failed to publish processed event",
				"document_id", doc.ID,
				"error", err,
			)
		}
	}

	msg.Ack()
	p.metrics.JobsProcessed.Add(1)
	p.metrics.LastProcessedAt.Store(time.Now())

	worker.logger.Info("document changed event processed",
		"event_id", event.EventID,
		"documents_found", len(documents),
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// handleDocumentProcessed handles document processed events.
func (p *WorkerPool) handleDocumentProcessed(ctx context.Context, worker *Worker, msg *nats.Msg) {
	start := time.Now()
	worker.processing.Store(true)
	p.metrics.CurrentActive.Add(1)
	defer func() {
		worker.processing.Store(false)
		p.metrics.CurrentActive.Add(-1)
		p.metrics.TotalLatencyMs.Add(time.Since(start).Milliseconds())
	}()

	var event DocumentProcessedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		worker.logger.Error("failed to unmarshal event", "error", err)
		msg.Nak()
		p.metrics.JobsFailed.Add(1)
		return
	}

	worker.logger.Info("processing document processed event",
		"event_id", event.EventID,
		"document_id", event.DocumentID,
	)

	// Generate embeddings if embedder is configured
	if p.embedder != nil && p.vectorDB != nil {
		// For now, just publish indexed event
		// In real implementation, would chunk content and generate embeddings
		worker.logger.Debug("embedder configured, would generate embeddings here")
	}

	// Publish indexed event
	indexedEvent := NewDocumentIndexedEvent(
		event.DocumentID,
		event.SourceURL,
		event.SourceType,
		event.Title,
		[]string{}, // Topics would be extracted from content
		[]string{}, // Cache keys to invalidate
	)

	if err := p.nats.PublishDocumentIndexed(ctx, indexedEvent); err != nil {
		worker.logger.Error("failed to publish indexed event",
			"document_id", event.DocumentID,
			"error", err,
		)
		msg.Nak()
		p.metrics.JobsFailed.Add(1)
		return
	}

	msg.Ack()
	p.metrics.JobsProcessed.Add(1)
	p.metrics.LastProcessedAt.Store(time.Now())

	worker.logger.Info("document processed event handled",
		"event_id", event.EventID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// handleDocumentIndexed handles document indexed events.
func (p *WorkerPool) handleDocumentIndexed(ctx context.Context, worker *Worker, msg *nats.Msg) {
	start := time.Now()
	worker.processing.Store(true)
	p.metrics.CurrentActive.Add(1)
	defer func() {
		worker.processing.Store(false)
		p.metrics.CurrentActive.Add(-1)
		p.metrics.TotalLatencyMs.Add(time.Since(start).Milliseconds())
	}()

	var event DocumentIndexedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		worker.logger.Error("failed to unmarshal event", "error", err)
		msg.Nak()
		p.metrics.JobsFailed.Add(1)
		return
	}

	worker.logger.Info("processing document indexed event",
		"event_id", event.EventID,
		"document_id", event.DocumentID,
		"affected_topics", event.AffectedTopics,
	)

	// Publish cache invalidation event if there are cache keys
	if len(event.CacheKeys) > 0 || len(event.AffectedTopics) > 0 {
		patterns := make([]string, len(event.AffectedTopics))
		for i, topic := range event.AffectedTopics {
			patterns[i] = "query_cache:" + topic + "*"
		}

		cacheEvent := NewCacheInvalidateEvent(
			event.CacheKeys,
			patterns,
			fmt.Sprintf("document_indexed:%s", event.DocumentID),
		)

		if err := p.nats.PublishCacheInvalidate(ctx, cacheEvent); err != nil {
			worker.logger.Error("failed to publish cache invalidate event",
				"document_id", event.DocumentID,
				"error", err,
			)
		}
	}

	msg.Ack()
	p.metrics.JobsProcessed.Add(1)
	p.metrics.LastProcessedAt.Store(time.Now())

	worker.logger.Info("document indexed event handled",
		"event_id", event.EventID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// GetMetrics returns current worker pool metrics.
func (p *WorkerPool) GetMetrics() map[string]interface{} {
	lastProcessed := p.metrics.LastProcessedAt.Load().(time.Time)

	return map[string]interface{}{
		"jobs_processed":    p.metrics.JobsProcessed.Load(),
		"jobs_failed":       p.metrics.JobsFailed.Load(),
		"jobs_retried":      p.metrics.JobsRetried.Load(),
		"current_active":    p.metrics.CurrentActive.Load(),
		"total_latency_ms":  p.metrics.TotalLatencyMs.Load(),
		"workers_count":     len(p.workers),
		"last_processed_at": lastProcessed,
	}
}

// GetActiveWorkers returns the number of currently active workers.
func (p *WorkerPool) GetActiveWorkers() int {
	count := 0
	for _, w := range p.workers {
		if w.processing.Load() {
			count++
		}
	}
	return count
}

// SubmitJob manually submits a job to be processed.
func (p *WorkerPool) SubmitJob(ctx context.Context, event DocumentChangedEvent) error {
	return p.nats.PublishDocumentChanged(ctx, event)
}
