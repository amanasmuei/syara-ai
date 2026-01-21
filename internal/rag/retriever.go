// Package rag provides Retrieval-Augmented Generation components.
package rag

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/google/uuid"
)

// Embedder defines the interface for generating embeddings.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// CacheManager defines the interface for caching operations.
type CacheManager interface {
	GetEmbedding(ctx context.Context, query string) ([]float32, bool, error)
	SetEmbedding(ctx context.Context, query string, embedding []float32) error
	GetRetrieval(ctx context.Context, key string) ([]storage.RetrievedChunk, bool, error)
	SetRetrieval(ctx context.Context, key string, chunks []storage.RetrievedChunk) error
}

// Retriever provides RAG retrieval with hybrid search capabilities.
type Retriever struct {
	vectorStore storage.VectorStore
	embedder    Embedder
	cache       CacheManager
	logger      *slog.Logger
	config      RetrieverConfig
}

// RetrieverConfig holds configuration for the retriever.
type RetrieverConfig struct {
	DefaultTopK        int
	DefaultMinScore    float64
	RRFConstant        int     // k constant for RRF (default: 60)
	SemanticWeight     float64 // Weight for semantic search results (0-1)
	KeywordWeight      float64 // Weight for keyword search results (0-1)
	EnableHybridSearch bool
	CacheEnabled       bool
}

// DefaultRetrieverConfig returns a default configuration.
func DefaultRetrieverConfig() RetrieverConfig {
	return RetrieverConfig{
		DefaultTopK:        10,
		DefaultMinScore:    0.5,
		RRFConstant:        60,
		SemanticWeight:     0.7,
		KeywordWeight:      0.3,
		EnableHybridSearch: true,
		CacheEnabled:       true,
	}
}

// RetrievalOptions represents options for retrieval.
type RetrievalOptions struct {
	TopK           int
	MinScore       float64
	SearchType     SearchType
	SourceType     string
	Categories     []string
	StandardNumber string
	DateRange      storage.DateRange
	DocumentIDs    []string
}

// SearchType defines the type of search to perform.
type SearchType string

const (
	SearchTypeSemantic SearchType = "semantic"
	SearchTypeKeyword  SearchType = "keyword"
	SearchTypeHybrid   SearchType = "hybrid"
)

// RetrievalResult represents the result of a retrieval operation.
type RetrievalResult struct {
	Chunks     []storage.RetrievedChunk `json:"chunks"`
	Query      string                   `json:"query"`
	SearchType SearchType               `json:"search_type"`
	Timing     RetrievalTiming          `json:"timing"`
	CacheHit   bool                     `json:"cache_hit"`
}

// RetrievalTiming tracks timing information for retrieval.
type RetrievalTiming struct {
	EmbeddingMs   int64 `json:"embedding_ms"`
	SemanticMs    int64 `json:"semantic_ms"`
	KeywordMs     int64 `json:"keyword_ms"`
	FusionMs      int64 `json:"fusion_ms"`
	TotalMs       int64 `json:"total_ms"`
	CacheLookupMs int64 `json:"cache_lookup_ms"`
}

// NewRetriever creates a new Retriever instance.
func NewRetriever(
	vectorStore storage.VectorStore,
	embedder Embedder,
	cache CacheManager,
	logger *slog.Logger,
	config RetrieverConfig,
) *Retriever {
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults if zero values
	if config.DefaultTopK == 0 {
		config.DefaultTopK = 10
	}
	if config.DefaultMinScore == 0 {
		config.DefaultMinScore = 0.5
	}
	if config.RRFConstant == 0 {
		config.RRFConstant = 60
	}
	if config.SemanticWeight == 0 {
		config.SemanticWeight = 0.7
	}
	if config.KeywordWeight == 0 {
		config.KeywordWeight = 0.3
	}

	return &Retriever{
		vectorStore: vectorStore,
		embedder:    embedder,
		cache:       cache,
		logger:      logger.With("component", "retriever"),
		config:      config,
	}
}

// Retrieve performs retrieval using the specified search type.
func (r *Retriever) Retrieve(ctx context.Context, query string, opts RetrievalOptions) (*RetrievalResult, error) {
	startTotal := time.Now()
	timing := RetrievalTiming{}

	// Apply defaults
	if opts.TopK <= 0 {
		opts.TopK = r.config.DefaultTopK
	}
	if opts.MinScore <= 0 {
		opts.MinScore = r.config.DefaultMinScore
	}
	if opts.SearchType == "" {
		if r.config.EnableHybridSearch {
			opts.SearchType = SearchTypeHybrid
		} else {
			opts.SearchType = SearchTypeSemantic
		}
	}

	r.logger.Info("starting retrieval",
		"query", query,
		"search_type", opts.SearchType,
		"top_k", opts.TopK,
	)

	var result *RetrievalResult

	switch opts.SearchType {
	case SearchTypeSemantic:
		result, timing = r.semanticSearch(ctx, query, opts)
	case SearchTypeKeyword:
		result, timing = r.keywordSearch(ctx, query, opts)
	case SearchTypeHybrid:
		result, timing = r.hybridSearch(ctx, query, opts)
	default:
		result, timing = r.hybridSearch(ctx, query, opts)
	}

	timing.TotalMs = time.Since(startTotal).Milliseconds()
	result.Timing = timing
	result.Query = query
	result.SearchType = opts.SearchType

	r.logger.Info("retrieval completed",
		"query", query,
		"results", len(result.Chunks),
		"total_ms", timing.TotalMs,
		"cache_hit", result.CacheHit,
	)

	return result, nil
}

// semanticSearch performs pure semantic vector search.
func (r *Retriever) semanticSearch(ctx context.Context, query string, opts RetrievalOptions) (*RetrievalResult, RetrievalTiming) {
	timing := RetrievalTiming{}
	result := &RetrievalResult{}

	// Get embedding (with cache check)
	embedding, cacheHit, embeddingMs := r.getQueryEmbedding(ctx, query)
	timing.EmbeddingMs = embeddingMs
	result.CacheHit = cacheHit

	if embedding == nil {
		r.logger.Error("failed to get query embedding", "query", query)
		return result, timing
	}

	// Perform semantic search
	startSemantic := time.Now()
	searchQuery := storage.SearchQuery{
		Embedding: embedding,
		TopK:      opts.TopK,
		MinScore:  opts.MinScore,
		Filters: storage.SearchFilters{
			SourceType:     opts.SourceType,
			Categories:     opts.Categories,
			StandardNumber: opts.StandardNumber,
			DateRange:      opts.DateRange,
			DocumentIDs:    opts.DocumentIDs,
		},
	}

	chunks, err := r.vectorStore.Search(ctx, searchQuery)
	timing.SemanticMs = time.Since(startSemantic).Milliseconds()

	if err != nil {
		r.logger.Error("semantic search failed", "error", err)
		return result, timing
	}

	result.Chunks = chunks
	return result, timing
}

// keywordSearch performs pure keyword/full-text search.
func (r *Retriever) keywordSearch(ctx context.Context, query string, opts RetrievalOptions) (*RetrievalResult, RetrievalTiming) {
	timing := RetrievalTiming{}
	result := &RetrievalResult{}

	startKeyword := time.Now()
	keywordOpts := storage.KeywordSearchOptions{
		TopK:       opts.TopK,
		SourceType: opts.SourceType,
		Categories: opts.Categories,
	}

	chunks, err := r.vectorStore.KeywordSearch(ctx, query, keywordOpts)
	timing.KeywordMs = time.Since(startKeyword).Milliseconds()

	if err != nil {
		r.logger.Error("keyword search failed", "error", err)
		return result, timing
	}

	result.Chunks = chunks
	return result, timing
}

// hybridSearch combines semantic and keyword search using RRF.
func (r *Retriever) hybridSearch(ctx context.Context, query string, opts RetrievalOptions) (*RetrievalResult, RetrievalTiming) {
	timing := RetrievalTiming{}
	result := &RetrievalResult{}

	// Get embedding (with cache check)
	embedding, cacheHit, embeddingMs := r.getQueryEmbedding(ctx, query)
	timing.EmbeddingMs = embeddingMs
	result.CacheHit = cacheHit

	if embedding == nil {
		r.logger.Error("failed to get query embedding, falling back to keyword search", "query", query)
		return r.keywordSearch(ctx, query, opts)
	}

	// Perform semantic search (get more results for fusion)
	startSemantic := time.Now()
	searchQuery := storage.SearchQuery{
		Embedding: embedding,
		TopK:      opts.TopK * 2, // Get more for fusion
		MinScore:  opts.MinScore * 0.8, // Slightly lower threshold for more candidates
		Filters: storage.SearchFilters{
			SourceType:     opts.SourceType,
			Categories:     opts.Categories,
			StandardNumber: opts.StandardNumber,
			DateRange:      opts.DateRange,
			DocumentIDs:    opts.DocumentIDs,
		},
	}

	semanticChunks, err := r.vectorStore.Search(ctx, searchQuery)
	timing.SemanticMs = time.Since(startSemantic).Milliseconds()

	if err != nil {
		r.logger.Error("semantic search failed in hybrid", "error", err)
	}

	// Perform keyword search
	startKeyword := time.Now()
	keywordOpts := storage.KeywordSearchOptions{
		TopK:       opts.TopK * 2, // Get more for fusion
		SourceType: opts.SourceType,
		Categories: opts.Categories,
	}

	keywordChunks, err := r.vectorStore.KeywordSearch(ctx, query, keywordOpts)
	timing.KeywordMs = time.Since(startKeyword).Milliseconds()

	if err != nil {
		r.logger.Error("keyword search failed in hybrid", "error", err)
	}

	// Reciprocal Rank Fusion
	startFusion := time.Now()
	fusedChunks := r.reciprocalRankFusion(semanticChunks, keywordChunks, opts.TopK)
	timing.FusionMs = time.Since(startFusion).Milliseconds()

	result.Chunks = fusedChunks
	return result, timing
}

// reciprocalRankFusion combines results from semantic and keyword search.
// RRF score = sum( 1 / (k + rank) ) where k is a constant (default: 60)
func (r *Retriever) reciprocalRankFusion(semantic, keyword []storage.RetrievedChunk, topK int) []storage.RetrievedChunk {
	// Map chunk ID to RRF score and chunk data
	type rrfItem struct {
		chunk storage.RetrievedChunk
		score float64
	}

	scores := make(map[uuid.UUID]*rrfItem)
	k := float64(r.config.RRFConstant)

	// Process semantic results
	for rank, chunk := range semantic {
		rrfScore := r.config.SemanticWeight * (1.0 / (k + float64(rank+1)))
		if existing, ok := scores[chunk.ID]; ok {
			existing.score += rrfScore
		} else {
			scores[chunk.ID] = &rrfItem{
				chunk: chunk,
				score: rrfScore,
			}
		}
	}

	// Process keyword results
	for rank, chunk := range keyword {
		rrfScore := r.config.KeywordWeight * (1.0 / (k + float64(rank+1)))
		if existing, ok := scores[chunk.ID]; ok {
			existing.score += rrfScore
			// Keep the higher similarity score
			if chunk.Similarity > existing.chunk.Similarity {
				existing.chunk.Similarity = chunk.Similarity
			}
		} else {
			scores[chunk.ID] = &rrfItem{
				chunk: chunk,
				score: rrfScore,
			}
		}
	}

	// Convert to slice and sort by RRF score
	results := make([]rrfItem, 0, len(scores))
	for _, item := range scores {
		results = append(results, *item)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Take top K results
	finalResults := make([]storage.RetrievedChunk, 0, topK)
	for i := 0; i < len(results) && i < topK; i++ {
		// Update similarity to reflect combined RRF score (normalized)
		results[i].chunk.Similarity = results[i].score * 100 // Scale for visibility
		finalResults = append(finalResults, results[i].chunk)
	}

	return finalResults
}

// getQueryEmbedding retrieves or generates an embedding for the query.
func (r *Retriever) getQueryEmbedding(ctx context.Context, query string) ([]float32, bool, int64) {
	start := time.Now()
	cacheHit := false

	// Check cache first
	if r.config.CacheEnabled && r.cache != nil {
		cachedEmbedding, hit, err := r.cache.GetEmbedding(ctx, query)
		if err == nil && hit {
			r.logger.Debug("embedding cache hit", "query", query)
			return cachedEmbedding, true, time.Since(start).Milliseconds()
		}
	}

	// Generate embedding
	if r.embedder == nil {
		r.logger.Error("embedder not configured")
		return nil, false, time.Since(start).Milliseconds()
	}

	embedding, err := r.embedder.Embed(ctx, query)
	if err != nil {
		r.logger.Error("failed to generate embedding", "error", err)
		return nil, false, time.Since(start).Milliseconds()
	}

	// Cache the embedding
	if r.config.CacheEnabled && r.cache != nil {
		if err := r.cache.SetEmbedding(ctx, query, embedding); err != nil {
			r.logger.Warn("failed to cache embedding", "error", err)
		}
	}

	return embedding, cacheHit, time.Since(start).Milliseconds()
}

// RetrieveWithEmbedding performs retrieval with a pre-computed embedding.
func (r *Retriever) RetrieveWithEmbedding(ctx context.Context, embedding []float32, opts RetrievalOptions) (*RetrievalResult, error) {
	startTotal := time.Now()
	timing := RetrievalTiming{}
	result := &RetrievalResult{}

	// Apply defaults
	if opts.TopK <= 0 {
		opts.TopK = r.config.DefaultTopK
	}
	if opts.MinScore <= 0 {
		opts.MinScore = r.config.DefaultMinScore
	}

	// Perform semantic search with provided embedding
	startSemantic := time.Now()
	searchQuery := storage.SearchQuery{
		Embedding: embedding,
		TopK:      opts.TopK,
		MinScore:  opts.MinScore,
		Filters: storage.SearchFilters{
			SourceType:     opts.SourceType,
			Categories:     opts.Categories,
			StandardNumber: opts.StandardNumber,
			DateRange:      opts.DateRange,
			DocumentIDs:    opts.DocumentIDs,
		},
	}

	chunks, err := r.vectorStore.Search(ctx, searchQuery)
	timing.SemanticMs = time.Since(startSemantic).Milliseconds()

	if err != nil {
		r.logger.Error("search with embedding failed", "error", err)
		return result, err
	}

	timing.TotalMs = time.Since(startTotal).Milliseconds()
	result.Chunks = chunks
	result.SearchType = SearchTypeSemantic
	result.Timing = timing

	return result, nil
}

// RetrieveSimilar finds chunks similar to a given chunk.
func (r *Retriever) RetrieveSimilar(ctx context.Context, chunkID string, topK int) (*RetrievalResult, error) {
	startTotal := time.Now()
	result := &RetrievalResult{}
	timing := RetrievalTiming{}

	// Get the source chunk
	chunk, err := r.vectorStore.GetByID(ctx, chunkID)
	if err != nil {
		return nil, err
	}
	if chunk == nil {
		return result, nil
	}

	// We need to get the embedding for this chunk
	// For now, we'll use the content to generate an embedding
	embedding, _, embeddingMs := r.getQueryEmbedding(ctx, chunk.Content)
	timing.EmbeddingMs = embeddingMs

	if embedding == nil {
		return result, nil
	}

	// Search for similar chunks, excluding the source
	startSemantic := time.Now()
	searchQuery := storage.SearchQuery{
		Embedding: embedding,
		TopK:      topK + 1, // Get one extra to exclude source
		MinScore:  0.5,
	}

	chunks, err := r.vectorStore.Search(ctx, searchQuery)
	timing.SemanticMs = time.Since(startSemantic).Milliseconds()

	if err != nil {
		return result, err
	}

	// Filter out the source chunk
	filtered := make([]storage.RetrievedChunk, 0, topK)
	for _, c := range chunks {
		if c.ID.String() != chunkID {
			filtered = append(filtered, c)
			if len(filtered) >= topK {
				break
			}
		}
	}

	timing.TotalMs = time.Since(startTotal).Milliseconds()
	result.Chunks = filtered
	result.SearchType = SearchTypeSemantic
	result.Timing = timing

	return result, nil
}
