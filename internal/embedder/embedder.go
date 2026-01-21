// Package embedder provides embedding generation services for text-to-vector conversion.
package embedder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	openai "github.com/sashabaranov/go-openai"
	"golang.org/x/time/rate"
)

// Embedder defines the interface for embedding generation.
type Embedder interface {
	// Embed generates an embedding for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embeddings for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the embedding dimension.
	Dimension() int

	// ModelName returns the model name.
	ModelName() string
}

// EmbedderConfig holds configuration for the embedder.
type EmbedderConfig struct {
	APIKey          string
	Model           string
	MaxBatchSize    int           // Max texts per batch (default: 100)
	MaxRetries      int           // Max retry attempts
	RetryDelay      time.Duration // Initial retry delay
	RateLimitRPS    int           // Requests per second
	EnableCache     bool          // Enable embedding caching
	CacheSize       int           // Max cache entries
	RequestTimeout  time.Duration // Timeout per request
}

// DefaultEmbedderConfig returns default configuration.
func DefaultEmbedderConfig(apiKey string) EmbedderConfig {
	return EmbedderConfig{
		APIKey:         apiKey,
		Model:          "text-embedding-3-small",
		MaxBatchSize:   100,
		MaxRetries:     3,
		RetryDelay:     time.Second,
		RateLimitRPS:   50,
		EnableCache:    true,
		CacheSize:      10000,
		RequestTimeout: 60 * time.Second,
	}
}

// OpenAIEmbedder implements embedding generation using OpenAI API.
type OpenAIEmbedder struct {
	client      *openai.Client
	config      EmbedderConfig
	rateLimiter *rate.Limiter
	cache       *embeddingCache
	log         *logger.Logger
	stats       *EmbedderStats
	statsMu     sync.RWMutex
}

// EmbedderStats tracks embedding usage statistics.
type EmbedderStats struct {
	TotalRequests    int64   `json:"total_requests"`
	TotalTokens      int64   `json:"total_tokens"`
	TotalTexts       int64   `json:"total_texts"`
	CacheHits        int64   `json:"cache_hits"`
	CacheMisses      int64   `json:"cache_misses"`
	Errors           int64   `json:"errors"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

// embeddingCache provides a simple LRU cache for embeddings.
type embeddingCache struct {
	entries  map[string]*cacheEntry
	order    []string
	maxSize  int
	mu       sync.RWMutex
}

type cacheEntry struct {
	embedding []float32
	createdAt time.Time
}

// NewOpenAIEmbedder creates a new OpenAI embedder.
func NewOpenAIEmbedder(cfg EmbedderConfig, log *logger.Logger) (*OpenAIEmbedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	if log == nil {
		log = logger.Default()
	}

	client := openai.NewClient(cfg.APIKey)

	var cache *embeddingCache
	if cfg.EnableCache {
		cache = &embeddingCache{
			entries: make(map[string]*cacheEntry),
			order:   make([]string, 0, cfg.CacheSize),
			maxSize: cfg.CacheSize,
		}
	}

	return &OpenAIEmbedder{
		client:      client,
		config:      cfg,
		rateLimiter: rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), cfg.RateLimitRPS),
		cache:       cache,
		log:         log.WithComponent("embedder"),
		stats:       &EmbedderStats{},
	}, nil
}

// Embed generates an embedding for a single text.
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

// EmbedBatch generates embeddings for multiple texts.
func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	startTime := time.Now()
	e.log.Debug("embedding batch", "count", len(texts))

	// Check cache for existing embeddings
	results := make([][]float32, len(texts))
	textsToEmbed := make([]string, 0, len(texts))
	textIndices := make([]int, 0, len(texts))

	if e.cache != nil {
		for i, text := range texts {
			if emb := e.cache.get(text); emb != nil {
				results[i] = emb
				e.incrementCacheHit()
			} else {
				textsToEmbed = append(textsToEmbed, text)
				textIndices = append(textIndices, i)
				e.incrementCacheMiss()
			}
		}
	} else {
		textsToEmbed = texts
		for i := range texts {
			textIndices = append(textIndices, i)
		}
	}

	// If all texts were cached, return immediately
	if len(textsToEmbed) == 0 {
		e.log.Debug("all embeddings from cache", "count", len(texts))
		return results, nil
	}

	// Process in batches
	for i := 0; i < len(textsToEmbed); i += e.config.MaxBatchSize {
		end := i + e.config.MaxBatchSize
		if end > len(textsToEmbed) {
			end = len(textsToEmbed)
		}

		batchTexts := textsToEmbed[i:end]
		batchIndices := textIndices[i:end]

		embeddings, tokens, err := e.embedBatchWithRetry(ctx, batchTexts)
		if err != nil {
			e.incrementError()
			return nil, fmt.Errorf("batch embedding failed: %w", err)
		}

		// Store results
		for j, emb := range embeddings {
			originalIdx := batchIndices[j]
			results[originalIdx] = emb

			// Cache the embedding
			if e.cache != nil {
				e.cache.set(batchTexts[j], emb)
			}
		}

		// Update stats
		e.updateStats(len(batchTexts), tokens, time.Since(startTime))
	}

	e.log.Info("batch embedding complete",
		"total_texts", len(texts),
		"from_cache", len(texts)-len(textsToEmbed),
		"from_api", len(textsToEmbed),
		"duration_ms", time.Since(startTime).Milliseconds(),
	)

	return results, nil
}

// embedBatchWithRetry performs the actual embedding call with retries.
func (e *OpenAIEmbedder) embedBatchWithRetry(ctx context.Context, texts []string) ([][]float32, int, error) {
	var lastErr error
	delay := e.config.RetryDelay

	for attempt := 0; attempt <= e.config.MaxRetries; attempt++ {
		if attempt > 0 {
			e.log.Debug("retrying embedding request", "attempt", attempt, "delay", delay)
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2 // Exponential backoff
		}

		// Wait for rate limiter
		if err := e.rateLimiter.Wait(ctx); err != nil {
			return nil, 0, fmt.Errorf("rate limiter error: %w", err)
		}

		embeddings, tokens, err := e.doEmbedBatch(ctx, texts)
		if err == nil {
			return embeddings, tokens, nil
		}

		lastErr = err
		e.log.WithError(err).Warn("embedding request failed", "attempt", attempt)
	}

	return nil, 0, fmt.Errorf("all retries failed: %w", lastErr)
}

// doEmbedBatch performs a single embedding API call.
func (e *OpenAIEmbedder) doEmbedBatch(ctx context.Context, texts []string) ([][]float32, int, error) {
	// Create request with timeout
	reqCtx, cancel := context.WithTimeout(ctx, e.config.RequestTimeout)
	defer cancel()

	req := openai.EmbeddingRequest{
		Input: texts,
		Model: openai.EmbeddingModel(e.config.Model),
	}

	resp, err := e.client.CreateEmbeddings(reqCtx, req)
	if err != nil {
		return nil, 0, fmt.Errorf("OpenAI API error: %w", err)
	}

	if len(resp.Data) != len(texts) {
		return nil, 0, fmt.Errorf("unexpected response: got %d embeddings for %d texts", len(resp.Data), len(texts))
	}

	// Extract embeddings
	embeddings := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		embeddings[i] = data.Embedding
	}

	return embeddings, resp.Usage.TotalTokens, nil
}

// Dimension returns the embedding dimension for the model.
func (e *OpenAIEmbedder) Dimension() int {
	switch e.config.Model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 1536
	}
}

// ModelName returns the model name.
func (e *OpenAIEmbedder) ModelName() string {
	return e.config.Model
}

// GetStats returns current embedding statistics.
func (e *OpenAIEmbedder) GetStats() EmbedderStats {
	e.statsMu.RLock()
	defer e.statsMu.RUnlock()
	return *e.stats
}

// ResetStats resets the statistics.
func (e *OpenAIEmbedder) ResetStats() {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	e.stats = &EmbedderStats{}
}

// Statistics update methods

func (e *OpenAIEmbedder) updateStats(textCount, tokens int, latency time.Duration) {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()

	e.stats.TotalRequests++
	e.stats.TotalTokens += int64(tokens)
	e.stats.TotalTexts += int64(textCount)

	// Calculate average latency (running average)
	totalLatency := e.stats.AvgLatencyMs * float64(e.stats.TotalRequests-1)
	e.stats.AvgLatencyMs = (totalLatency + float64(latency.Milliseconds())) / float64(e.stats.TotalRequests)

	// Estimate cost ($0.02 per 1M tokens for text-embedding-3-small)
	e.stats.EstimatedCostUSD = float64(e.stats.TotalTokens) * 0.00000002
}

func (e *OpenAIEmbedder) incrementCacheHit() {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	e.stats.CacheHits++
}

func (e *OpenAIEmbedder) incrementCacheMiss() {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	e.stats.CacheMisses++
}

func (e *OpenAIEmbedder) incrementError() {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	e.stats.Errors++
}

// Cache methods

func (c *embeddingCache) get(text string) []float32 {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	key := hashText(text)
	entry, ok := c.entries[key]
	if !ok {
		return nil
	}

	return entry.embedding
}

func (c *embeddingCache) set(text string, embedding []float32) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := hashText(text)

	// Check if already exists
	if _, exists := c.entries[key]; exists {
		return
	}

	// Evict oldest if at capacity
	if len(c.entries) >= c.maxSize && c.maxSize > 0 {
		if len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
	}

	// Add new entry
	c.entries[key] = &cacheEntry{
		embedding: embedding,
		createdAt: time.Now(),
	}
	c.order = append(c.order, key)
}

func (c *embeddingCache) size() int {
	if c == nil {
		return 0
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *embeddingCache) clear() {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
	c.order = make([]string, 0, c.maxSize)
}

// hashText generates a hash key for caching.
func hashText(text string) string {
	hash := sha256.Sum256([]byte(text))
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes for shorter key
}

// MockEmbedder provides a mock embedder for testing.
type MockEmbedder struct {
	dimension int
	delay     time.Duration
}

// NewMockEmbedder creates a new mock embedder.
func NewMockEmbedder(dimension int) *MockEmbedder {
	return &MockEmbedder{
		dimension: dimension,
		delay:     10 * time.Millisecond,
	}
}

// Embed generates a mock embedding.
func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	time.Sleep(m.delay)

	// Generate deterministic embedding based on text hash
	hash := sha256.Sum256([]byte(text))
	embedding := make([]float32, m.dimension)

	for i := 0; i < m.dimension; i++ {
		// Use hash bytes to generate pseudo-random values
		byteIdx := i % 32
		embedding[i] = float32(hash[byteIdx]) / 255.0
	}

	return embedding, nil
}

// EmbedBatch generates mock embeddings for multiple texts.
func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings := make([][]float32, len(texts))

	for i, text := range texts {
		emb, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		embeddings[i] = emb
	}

	return embeddings, nil
}

// Dimension returns the mock embedding dimension.
func (m *MockEmbedder) Dimension() int {
	return m.dimension
}

// ModelName returns the mock model name.
func (m *MockEmbedder) ModelName() string {
	return "mock-embedder"
}

// CosineSimilarity calculates cosine similarity between two embeddings.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrt(normA) * sqrt(normB))
}

// sqrt is a simple square root approximation for float32.
func sqrt(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Newton's method
	z := x / 2
	for i := 0; i < 10; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}

// NormalizeEmbedding normalizes an embedding to unit length.
func NormalizeEmbedding(embedding []float32) []float32 {
	var norm float32
	for _, v := range embedding {
		norm += v * v
	}
	norm = sqrt(norm)

	if norm == 0 {
		return embedding
	}

	result := make([]float32, len(embedding))
	for i, v := range embedding {
		result[i] = v / norm
	}
	return result
}
