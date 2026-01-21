package rag

import (
	"context"
	"testing"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/google/uuid"
)

// MockVectorStore implements storage.VectorStore for testing.
type MockVectorStore struct {
	searchResults   []storage.RetrievedChunk
	keywordResults  []storage.RetrievedChunk
	getByIDResult   *storage.DocumentChunk
	searchErr       error
	keywordErr      error
	searchCalled    bool
	keywordCalled   bool
	lastSearchQuery storage.SearchQuery
}

func (m *MockVectorStore) Upsert(ctx context.Context, chunk storage.DocumentChunk) error {
	return nil
}

func (m *MockVectorStore) UpsertBatch(ctx context.Context, chunks []storage.DocumentChunk) error {
	return nil
}

func (m *MockVectorStore) Search(ctx context.Context, query storage.SearchQuery) ([]storage.RetrievedChunk, error) {
	m.searchCalled = true
	m.lastSearchQuery = query
	return m.searchResults, m.searchErr
}

func (m *MockVectorStore) SearchBySource(ctx context.Context, query storage.SearchQuery, sourceType string) ([]storage.RetrievedChunk, error) {
	return m.Search(ctx, query)
}

func (m *MockVectorStore) Delete(ctx context.Context, chunkID string) error {
	return nil
}

func (m *MockVectorStore) DeleteByDocument(ctx context.Context, documentID string) error {
	return nil
}

func (m *MockVectorStore) GetByID(ctx context.Context, chunkID string) (*storage.DocumentChunk, error) {
	return m.getByIDResult, nil
}

func (m *MockVectorStore) KeywordSearch(ctx context.Context, query string, opts storage.KeywordSearchOptions) ([]storage.RetrievedChunk, error) {
	m.keywordCalled = true
	return m.keywordResults, m.keywordErr
}

func (m *MockVectorStore) Health(ctx context.Context) error {
	return nil
}

// MockEmbedder implements Embedder for testing.
type MockEmbedder struct {
	embedding []float32
	err       error
	called    bool
}

func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	m.called = true
	return m.embedding, m.err
}

func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i := range texts {
		results[i] = m.embedding
	}
	return results, m.err
}

// MockCacheManager implements CacheManager for testing.
type MockCacheManager struct {
	embeddingCache map[string][]float32
	retrievalCache map[string][]storage.RetrievedChunk
	getCalled      bool
	setCalled      bool
	embeddingHit   bool
}

func NewMockCacheManager() *MockCacheManager {
	return &MockCacheManager{
		embeddingCache: make(map[string][]float32),
		retrievalCache: make(map[string][]storage.RetrievedChunk),
	}
}

func (m *MockCacheManager) GetEmbedding(ctx context.Context, query string) ([]float32, bool, error) {
	m.getCalled = true
	emb, ok := m.embeddingCache[query]
	m.embeddingHit = ok
	return emb, ok, nil
}

func (m *MockCacheManager) SetEmbedding(ctx context.Context, query string, embedding []float32) error {
	m.setCalled = true
	m.embeddingCache[query] = embedding
	return nil
}

func (m *MockCacheManager) GetRetrieval(ctx context.Context, key string) ([]storage.RetrievedChunk, bool, error) {
	chunks, ok := m.retrievalCache[key]
	return chunks, ok, nil
}

func (m *MockCacheManager) SetRetrieval(ctx context.Context, key string, chunks []storage.RetrievedChunk) error {
	m.retrievalCache[key] = chunks
	return nil
}

// Helper to create test chunks
func createTestChunks(count int, sourceType string) []storage.RetrievedChunk {
	chunks := make([]storage.RetrievedChunk, count)
	for i := 0; i < count; i++ {
		chunks[i] = storage.RetrievedChunk{
			DocumentChunk: storage.DocumentChunk{
				ID:           uuid.New(),
				DocumentID:   uuid.New(),
				Content:      "Test content for chunk",
				PageNumber:   i + 1,
				SectionTitle: "Test Section",
				ChunkIndex:   i,
			},
			Similarity:    0.9 - float64(i)*0.05,
			SourceType:    sourceType,
			DocumentTitle: "Test Document",
			Category:      "test",
		}
	}
	return chunks
}

// Helper to create test embedding
func createTestEmbedding(dim int) []float32 {
	emb := make([]float32, dim)
	for i := range emb {
		emb[i] = float32(i) * 0.001
	}
	return emb
}

func TestNewRetriever(t *testing.T) {
	store := &MockVectorStore{}
	embedder := &MockEmbedder{}
	cache := NewMockCacheManager()
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, cache, nil, config)

	if retriever == nil {
		t.Fatal("expected retriever to be created")
	}
	if retriever.config.DefaultTopK != 10 {
		t.Errorf("expected DefaultTopK 10, got %d", retriever.config.DefaultTopK)
	}
	if retriever.config.SemanticWeight != 0.7 {
		t.Errorf("expected SemanticWeight 0.7, got %f", retriever.config.SemanticWeight)
	}
}

func TestNewRetriever_DefaultsZeroValues(t *testing.T) {
	store := &MockVectorStore{}
	embedder := &MockEmbedder{}
	config := RetrieverConfig{} // All zero values

	retriever := NewRetriever(store, embedder, nil, nil, config)

	if retriever.config.DefaultTopK != 10 {
		t.Errorf("expected DefaultTopK 10, got %d", retriever.config.DefaultTopK)
	}
	if retriever.config.DefaultMinScore != 0.5 {
		t.Errorf("expected DefaultMinScore 0.5, got %f", retriever.config.DefaultMinScore)
	}
	if retriever.config.RRFConstant != 60 {
		t.Errorf("expected RRFConstant 60, got %d", retriever.config.RRFConstant)
	}
}

func TestRetriever_SemanticSearch(t *testing.T) {
	ctx := context.Background()
	testChunks := createTestChunks(5, "bnm")
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: testChunks}
	embedder := &MockEmbedder{embedding: testEmbedding}
	cache := NewMockCacheManager()
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, cache, nil, config)

	result, err := retriever.Retrieve(ctx, "murabaha ownership requirements", RetrievalOptions{
		TopK:       5,
		MinScore:   0.5,
		SearchType: SearchTypeSemantic,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if len(result.Chunks) != 5 {
		t.Errorf("expected 5 chunks, got %d", len(result.Chunks))
	}
	if result.SearchType != SearchTypeSemantic {
		t.Errorf("expected SearchTypeSemantic, got %s", result.SearchType)
	}
	if !store.searchCalled {
		t.Error("expected vector store Search to be called")
	}
	if !embedder.called {
		t.Error("expected embedder to be called")
	}
}

func TestRetriever_KeywordSearch(t *testing.T) {
	ctx := context.Background()
	testChunks := createTestChunks(3, "aaoifi")

	store := &MockVectorStore{keywordResults: testChunks}
	embedder := &MockEmbedder{}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	result, err := retriever.Retrieve(ctx, "islamic banking", RetrievalOptions{
		TopK:       10,
		SearchType: SearchTypeKeyword,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(result.Chunks))
	}
	if result.SearchType != SearchTypeKeyword {
		t.Errorf("expected SearchTypeKeyword, got %s", result.SearchType)
	}
	if !store.keywordCalled {
		t.Error("expected keyword search to be called")
	}
	if embedder.called {
		t.Error("embedder should not be called for keyword search")
	}
}

func TestRetriever_HybridSearch(t *testing.T) {
	ctx := context.Background()
	semanticChunks := createTestChunks(5, "bnm")
	keywordChunks := createTestChunks(5, "bnm")
	testEmbedding := createTestEmbedding(1536)

	// Make some chunks overlap (same ID) to test RRF fusion
	keywordChunks[0].ID = semanticChunks[0].ID
	keywordChunks[1].ID = semanticChunks[2].ID

	store := &MockVectorStore{
		searchResults:  semanticChunks,
		keywordResults: keywordChunks,
	}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()
	config.EnableHybridSearch = true

	retriever := NewRetriever(store, embedder, nil, nil, config)

	result, err := retriever.Retrieve(ctx, "murabaha financing requirements", RetrievalOptions{
		TopK:       5,
		SearchType: SearchTypeHybrid,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SearchType != SearchTypeHybrid {
		t.Errorf("expected SearchTypeHybrid, got %s", result.SearchType)
	}
	if !store.searchCalled {
		t.Error("expected vector search to be called")
	}
	if !store.keywordCalled {
		t.Error("expected keyword search to be called")
	}
	// Should have less than 10 results due to deduplication
	if len(result.Chunks) > 10 {
		t.Errorf("expected at most 10 chunks after fusion, got %d", len(result.Chunks))
	}
}

func TestRetriever_DefaultsToHybridSearch(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{
		searchResults:  createTestChunks(3, "bnm"),
		keywordResults: createTestChunks(3, "bnm"),
	}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()
	config.EnableHybridSearch = true

	retriever := NewRetriever(store, embedder, nil, nil, config)

	result, err := retriever.Retrieve(ctx, "test query", RetrievalOptions{
		TopK: 5,
		// SearchType not specified
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SearchType != SearchTypeHybrid {
		t.Errorf("expected SearchTypeHybrid as default, got %s", result.SearchType)
	}
}

func TestRetriever_AppliesDefaultTopK(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: createTestChunks(10, "bnm")}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	_, err := retriever.Retrieve(ctx, "test query", RetrievalOptions{
		SearchType: SearchTypeSemantic,
		// TopK not specified
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Check the search query had default TopK applied
	if store.lastSearchQuery.TopK != 10 {
		t.Errorf("expected default TopK 10, got %d", store.lastSearchQuery.TopK)
	}
}

func TestRetriever_WithFilters(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: createTestChunks(5, "bnm")}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	_, err := retriever.Retrieve(ctx, "murabaha", RetrievalOptions{
		TopK:           5,
		SearchType:     SearchTypeSemantic,
		SourceType:     "bnm",
		Categories:     []string{"policy_documents", "circulars"},
		StandardNumber: "SS-8",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify filters were passed
	if store.lastSearchQuery.Filters.SourceType != "bnm" {
		t.Errorf("expected SourceType 'bnm', got '%s'", store.lastSearchQuery.Filters.SourceType)
	}
	if len(store.lastSearchQuery.Filters.Categories) != 2 {
		t.Errorf("expected 2 categories, got %d", len(store.lastSearchQuery.Filters.Categories))
	}
	if store.lastSearchQuery.Filters.StandardNumber != "SS-8" {
		t.Errorf("expected StandardNumber 'SS-8', got '%s'", store.lastSearchQuery.Filters.StandardNumber)
	}
}

func TestRetriever_CacheHit(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: createTestChunks(3, "bnm")}
	embedder := &MockEmbedder{embedding: testEmbedding}
	cache := NewMockCacheManager()
	// Pre-populate cache
	cache.embeddingCache["cached query"] = testEmbedding

	config := DefaultRetrieverConfig()
	config.CacheEnabled = true

	retriever := NewRetriever(store, embedder, cache, nil, config)

	result, err := retriever.Retrieve(ctx, "cached query", RetrievalOptions{
		SearchType: SearchTypeSemantic,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.CacheHit {
		t.Error("expected cache hit")
	}
	if embedder.called {
		t.Error("embedder should not be called on cache hit")
	}
}

func TestRetriever_CacheMiss(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: createTestChunks(3, "bnm")}
	embedder := &MockEmbedder{embedding: testEmbedding}
	cache := NewMockCacheManager()

	config := DefaultRetrieverConfig()
	config.CacheEnabled = true

	retriever := NewRetriever(store, embedder, cache, nil, config)

	result, err := retriever.Retrieve(ctx, "new query", RetrievalOptions{
		SearchType: SearchTypeSemantic,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CacheHit {
		t.Error("expected cache miss")
	}
	if !embedder.called {
		t.Error("embedder should be called on cache miss")
	}
	if !cache.setCalled {
		t.Error("cache set should be called after generating embedding")
	}
}

func TestRetriever_RecordsTiming(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: createTestChunks(3, "bnm")}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	result, err := retriever.Retrieve(ctx, "test query", RetrievalOptions{
		SearchType: SearchTypeSemantic,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Timing.TotalMs < 0 {
		t.Error("expected TotalMs >= 0")
	}
	if result.Timing.EmbeddingMs < 0 {
		t.Error("expected EmbeddingMs >= 0")
	}
}

func TestRetriever_RetrieveWithEmbedding(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)
	testChunks := createTestChunks(5, "aaoifi")

	store := &MockVectorStore{searchResults: testChunks}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, nil, nil, nil, config)

	result, err := retriever.RetrieveWithEmbedding(ctx, testEmbedding, RetrievalOptions{
		TopK:     5,
		MinScore: 0.5,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Chunks) != 5 {
		t.Errorf("expected 5 chunks, got %d", len(result.Chunks))
	}
	if result.SearchType != SearchTypeSemantic {
		t.Errorf("expected SearchTypeSemantic, got %s", result.SearchType)
	}
}

func TestRetriever_RetrieveSimilar(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)
	testChunks := createTestChunks(6, "bnm")
	sourceChunk := &storage.DocumentChunk{
		ID:      testChunks[0].ID,
		Content: "Source chunk content",
	}

	store := &MockVectorStore{
		searchResults: testChunks,
		getByIDResult: sourceChunk,
	}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	result, err := retriever.RetrieveSimilar(ctx, sourceChunk.ID.String(), 5)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should exclude the source chunk
	for _, chunk := range result.Chunks {
		if chunk.ID == sourceChunk.ID {
			t.Error("source chunk should be excluded from results")
		}
	}
}

func TestReciprocalRankFusion(t *testing.T) {
	config := DefaultRetrieverConfig()
	retriever := &Retriever{config: config}

	// Create overlapping results
	id1, id2, id3, id4 := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	semantic := []storage.RetrievedChunk{
		{DocumentChunk: storage.DocumentChunk{ID: id1}, Similarity: 0.95},
		{DocumentChunk: storage.DocumentChunk{ID: id2}, Similarity: 0.90},
		{DocumentChunk: storage.DocumentChunk{ID: id3}, Similarity: 0.85},
	}

	keyword := []storage.RetrievedChunk{
		{DocumentChunk: storage.DocumentChunk{ID: id2}, Similarity: 0.8}, // Overlaps with semantic
		{DocumentChunk: storage.DocumentChunk{ID: id4}, Similarity: 0.7},
		{DocumentChunk: storage.DocumentChunk{ID: id1}, Similarity: 0.6}, // Overlaps with semantic
	}

	results := retriever.reciprocalRankFusion(semantic, keyword, 5)

	if len(results) != 4 {
		t.Errorf("expected 4 unique chunks, got %d", len(results))
	}

	// Verify deduplication worked - all IDs should be unique
	seen := make(map[uuid.UUID]bool)
	for _, r := range results {
		if seen[r.ID] {
			t.Errorf("duplicate ID found: %s", r.ID)
		}
		seen[r.ID] = true
	}
}

func TestRetriever_DateRangeFilter(t *testing.T) {
	ctx := context.Background()
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: createTestChunks(3, "bnm")}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	_, err := retriever.Retrieve(ctx, "test query", RetrievalOptions{
		SearchType: SearchTypeSemantic,
		DateRange: storage.DateRange{
			Start: startDate,
			End:   endDate,
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store.lastSearchQuery.Filters.DateRange.Start != startDate {
		t.Errorf("expected start date %v, got %v", startDate, store.lastSearchQuery.Filters.DateRange.Start)
	}
	if store.lastSearchQuery.Filters.DateRange.End != endDate {
		t.Errorf("expected end date %v, got %v", endDate, store.lastSearchQuery.Filters.DateRange.End)
	}
}

func TestDefaultRetrieverConfig(t *testing.T) {
	config := DefaultRetrieverConfig()

	if config.DefaultTopK != 10 {
		t.Errorf("expected DefaultTopK 10, got %d", config.DefaultTopK)
	}
	if config.DefaultMinScore != 0.5 {
		t.Errorf("expected DefaultMinScore 0.5, got %f", config.DefaultMinScore)
	}
	if config.RRFConstant != 60 {
		t.Errorf("expected RRFConstant 60, got %d", config.RRFConstant)
	}
	if config.SemanticWeight != 0.7 {
		t.Errorf("expected SemanticWeight 0.7, got %f", config.SemanticWeight)
	}
	if config.KeywordWeight != 0.3 {
		t.Errorf("expected KeywordWeight 0.3, got %f", config.KeywordWeight)
	}
	if !config.EnableHybridSearch {
		t.Error("expected EnableHybridSearch true")
	}
	if !config.CacheEnabled {
		t.Error("expected CacheEnabled true")
	}
}

func BenchmarkRetriever_SemanticSearch(b *testing.B) {
	ctx := context.Background()
	testChunks := createTestChunks(10, "bnm")
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{searchResults: testChunks}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = retriever.Retrieve(ctx, "test query", RetrievalOptions{
			TopK:       10,
			SearchType: SearchTypeSemantic,
		})
	}
}

func BenchmarkRetriever_HybridSearch(b *testing.B) {
	ctx := context.Background()
	testChunks := createTestChunks(10, "bnm")
	testEmbedding := createTestEmbedding(1536)

	store := &MockVectorStore{
		searchResults:  testChunks,
		keywordResults: testChunks,
	}
	embedder := &MockEmbedder{embedding: testEmbedding}
	config := DefaultRetrieverConfig()

	retriever := NewRetriever(store, embedder, nil, nil, config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = retriever.Retrieve(ctx, "test query", RetrievalOptions{
			TopK:       10,
			SearchType: SearchTypeHybrid,
		})
	}
}
