// Package evaluation provides RAG evaluation metrics and benchmarking tools.
package evaluation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockRetriever implements Retriever interface for testing.
type MockRetriever struct {
	results    []RetrievalResult
	err        error
	delay      time.Duration
	callCount  int
	lastQuery  string
	lastTopK   int
}

func NewMockRetriever() *MockRetriever {
	return &MockRetriever{
		results: []RetrievalResult{
			{ID: "doc1", Score: 0.95},
			{ID: "doc2", Score: 0.85},
			{ID: "doc3", Score: 0.75},
			{ID: "doc4", Score: 0.65},
			{ID: "doc5", Score: 0.55},
		},
	}
}

func (m *MockRetriever) Retrieve(ctx context.Context, query string, topK int) ([]RetrievalResult, error) {
	m.callCount++
	m.lastQuery = query
	m.lastTopK = topK

	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if m.err != nil {
		return nil, m.err
	}

	if topK > len(m.results) {
		return m.results, nil
	}
	return m.results[:topK], nil
}

func TestBenchmarkRunner_Run(t *testing.T) {
	retriever := NewMockRetriever()
	retriever.results = []RetrievalResult{
		{ID: "doc1", Score: 0.95},
		{ID: "doc2", Score: 0.85},
		{ID: "doc3", Score: 0.75},
	}

	config := DefaultBenchmarkConfig()
	runner := NewBenchmarkRunner(retriever, config, nil)

	dataset := BenchmarkDataset{
		Name: "test-dataset",
		Queries: []BenchmarkQuery{
			{
				ID:             "q1",
				Query:          "test query 1",
				Category:       "cat1",
				RelevantDocIDs: []string{"doc1", "doc2"},
			},
			{
				ID:             "q2",
				Query:          "test query 2",
				Category:       "cat1",
				RelevantDocIDs: []string{"doc1"},
			},
			{
				ID:             "q3",
				Query:          "test query 3",
				Category:       "cat2",
				RelevantDocIDs: []string{"doc3"},
			},
		},
	}

	ctx := context.Background()
	result, err := runner.Run(ctx, dataset)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify basic result properties
	assert.Equal(t, "test-dataset", result.DatasetName)
	assert.Equal(t, 3, len(result.QueryResults))
	assert.Empty(t, result.Errors)

	// Verify metrics are calculated
	assert.Equal(t, 3, result.Metrics.TotalQueries)
	assert.GreaterOrEqual(t, result.Metrics.MRR, 0.0)
	assert.LessOrEqual(t, result.Metrics.MRR, 1.0)

	// Verify by-category metrics
	assert.Len(t, result.ByCategory, 2)
	assert.Contains(t, result.ByCategory, "cat1")
	assert.Contains(t, result.ByCategory, "cat2")
}

func TestBenchmarkRunner_Run_WithErrors(t *testing.T) {
	retriever := NewMockRetriever()
	retriever.err = errors.New("retrieval failed")

	config := DefaultBenchmarkConfig()
	runner := NewBenchmarkRunner(retriever, config, nil)

	dataset := BenchmarkDataset{
		Name: "test-dataset",
		Queries: []BenchmarkQuery{
			{
				ID:             "q1",
				Query:          "test query",
				RelevantDocIDs: []string{"doc1"},
			},
		},
	}

	ctx := context.Background()
	result, err := runner.Run(ctx, dataset)

	require.NoError(t, err) // Run itself doesn't fail, just records errors
	require.NotNil(t, result)

	assert.Len(t, result.Errors, 1)
	assert.Equal(t, "q1", result.Errors[0].QueryID)
	assert.Contains(t, result.Errors[0].Error, "retrieval failed")
}

func TestBenchmarkRunner_Run_WithTimeout(t *testing.T) {
	retriever := NewMockRetriever()
	retriever.delay = 2 * time.Second // Longer than timeout

	config := DefaultBenchmarkConfig()
	config.QueryTimeout = 100 * time.Millisecond
	runner := NewBenchmarkRunner(retriever, config, nil)

	dataset := BenchmarkDataset{
		Name: "test-dataset",
		Queries: []BenchmarkQuery{
			{
				ID:             "q1",
				Query:          "test query",
				RelevantDocIDs: []string{"doc1"},
			},
		},
	}

	ctx := context.Background()
	result, err := runner.Run(ctx, dataset)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Query should have timed out
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Error, "context deadline exceeded")
}

func TestBenchmarkRunner_Run_EmptyDataset(t *testing.T) {
	retriever := NewMockRetriever()
	config := DefaultBenchmarkConfig()
	runner := NewBenchmarkRunner(retriever, config, nil)

	dataset := BenchmarkDataset{
		Name:    "empty-dataset",
		Queries: []BenchmarkQuery{},
	}

	ctx := context.Background()
	result, err := runner.Run(ctx, dataset)

	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "empty-dataset", result.DatasetName)
	assert.Empty(t, result.QueryResults)
	assert.Equal(t, 0, result.Metrics.TotalQueries)
}

func TestDefaultBenchmarkConfig(t *testing.T) {
	config := DefaultBenchmarkConfig()

	assert.Equal(t, []int{1, 3, 5, 10}, config.TopKValues)
	assert.Equal(t, 4, config.Workers)
	assert.Equal(t, 30*time.Second, config.QueryTimeout)
	assert.True(t, config.MeasureLatency)
	assert.Equal(t, "json", config.OutputFormat)
}

func TestCreateIslamicBankingBenchmark(t *testing.T) {
	dataset := CreateIslamicBankingBenchmark()

	assert.Equal(t, "islamic-banking-benchmark-v1", dataset.Name)
	assert.NotEmpty(t, dataset.Description)
	assert.Equal(t, "1.0.0", dataset.Version)
	assert.NotEmpty(t, dataset.Queries)
	assert.NotEmpty(t, dataset.Categories)

	// Verify categories
	expectedCategories := []string{
		"musharakah",
		"murabaha",
		"ijara",
		"sukuk",
		"takaful",
		"general_shariah",
		"bnm_regulations",
		"aaoifi_standards",
	}
	assert.Equal(t, expectedCategories, dataset.Categories)

	// Verify queries have required fields
	for _, q := range dataset.Queries {
		assert.NotEmpty(t, q.ID, "query ID should not be empty")
		assert.NotEmpty(t, q.Query, "query should not be empty")
		assert.NotEmpty(t, q.Category, "category should not be empty")
	}

	// Verify we have queries in each category
	categoryCounts := make(map[string]int)
	for _, q := range dataset.Queries {
		categoryCounts[q.Category]++
	}

	for _, cat := range expectedCategories {
		assert.Greater(t, categoryCounts[cat], 0, "should have queries for category: "+cat)
	}
}

func TestFormatResultsMarkdown(t *testing.T) {
	result := &BenchmarkResult{
		DatasetName: "test-benchmark",
		Timestamp:   time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Metrics: EvaluationMetrics{
			PrecisionAt1:  0.8,
			PrecisionAt5:  0.6,
			RecallAt5:     0.75,
			HitRateAt5:    0.9,
			MRR:           0.85,
			MAP:           0.72,
			NDCGAt5:       0.78,
			NDCGAt10:      0.76,
			MeanLatencyMs: 150.5,
			MedianLatencyMs: 140.0,
			P95LatencyMs:  250.0,
			P99LatencyMs:  300.0,
			TotalQueries:  100,
		},
		ByCategory: map[string]EvaluationMetrics{
			"musharakah": {
				MRR:         0.9,
				PrecisionAt5: 0.7,
				RecallAt5:   0.8,
				HitRateAt5:  0.95,
			},
			"murabaha": {
				MRR:         0.8,
				PrecisionAt5: 0.5,
				RecallAt5:   0.6,
				HitRateAt5:  0.85,
			},
		},
		Errors: []BenchmarkError{
			{QueryID: "q1", Error: "timeout"},
		},
	}

	md := FormatResultsMarkdown(result)

	// Verify markdown contains expected sections
	assert.Contains(t, md, "# Benchmark Results: test-benchmark")
	assert.Contains(t, md, "## Overall Metrics")
	assert.Contains(t, md, "### Precision")
	assert.Contains(t, md, "### Recall")
	assert.Contains(t, md, "### Hit Rate")
	assert.Contains(t, md, "### Latency")
	assert.Contains(t, md, "## Results by Category")
	assert.Contains(t, md, "musharakah")
	assert.Contains(t, md, "murabaha")
	assert.Contains(t, md, "## Errors")
	assert.Contains(t, md, "timeout")

	// Verify metric values are present
	assert.Contains(t, md, "0.8500") // MRR
	assert.Contains(t, md, "0.7200") // MAP
}

func TestBenchmarkRunner_RunComparison(t *testing.T) {
	retriever1 := NewMockRetriever()
	retriever1.results = []RetrievalResult{
		{ID: "doc1", Score: 0.95},
		{ID: "doc2", Score: 0.85},
	}

	retriever2 := NewMockRetriever()
	retriever2.results = []RetrievalResult{
		{ID: "doc2", Score: 0.90},
		{ID: "doc1", Score: 0.80},
	}

	config := DefaultBenchmarkConfig()
	runner := NewBenchmarkRunner(retriever1, config, nil)

	dataset := BenchmarkDataset{
		Name: "comparison-test",
		Queries: []BenchmarkQuery{
			{
				ID:             "q1",
				Query:          "test query",
				RelevantDocIDs: []string{"doc1"},
			},
		},
	}

	retrievers := map[string]Retriever{
		"retriever1": retriever1,
		"retriever2": retriever2,
	}

	ctx := context.Background()
	results, err := runner.RunComparison(ctx, dataset, retrievers)

	require.NoError(t, err)
	require.Len(t, results, 2)

	// Retriever1 should have better MRR (doc1 at position 1)
	assert.Greater(t, results["retriever1"].Metrics.MRR, results["retriever2"].Metrics.MRR)
}

// Benchmark tests
func BenchmarkBenchmarkRunner_Run(b *testing.B) {
	retriever := NewMockRetriever()
	config := DefaultBenchmarkConfig()
	runner := NewBenchmarkRunner(retriever, config, nil)

	// Create a dataset with 50 queries
	queries := make([]BenchmarkQuery, 50)
	for i := range queries {
		queries[i] = BenchmarkQuery{
			ID:             string(rune('a' + i)),
			Query:          "benchmark query",
			Category:       "benchmark",
			RelevantDocIDs: []string{"doc1", "doc2"},
		}
	}

	dataset := BenchmarkDataset{
		Name:    "benchmark-dataset",
		Queries: queries,
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runner.Run(ctx, dataset)
	}
}

func BenchmarkFormatResultsMarkdown(b *testing.B) {
	result := &BenchmarkResult{
		DatasetName: "benchmark",
		Timestamp:   time.Now(),
		Metrics: EvaluationMetrics{
			PrecisionAt1:  0.8,
			PrecisionAt5:  0.6,
			MRR:           0.85,
			MeanLatencyMs: 100,
		},
		ByCategory: map[string]EvaluationMetrics{
			"cat1": {MRR: 0.9},
			"cat2": {MRR: 0.8},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FormatResultsMarkdown(result)
	}
}

// Integration test simulating a real benchmark workflow
func TestBenchmarkWorkflow_Integration(t *testing.T) {
	// Create a mock retriever that returns different results based on query
	retriever := &MockRetriever{
		results: []RetrievalResult{
			{ID: "bnm-musharakah-001", Score: 0.92},
			{ID: "aaoifi-partnership-003", Score: 0.88},
			{ID: "bnm-musharakah-002", Score: 0.85},
			{ID: "generic-doc-001", Score: 0.70},
			{ID: "unrelated-doc", Score: 0.50},
		},
	}

	config := DefaultBenchmarkConfig()
	config.TopKValues = []int{1, 3, 5}
	runner := NewBenchmarkRunner(retriever, config, nil)

	// Use the Islamic banking benchmark dataset
	dataset := CreateIslamicBankingBenchmark()

	// Assign relevant document IDs to some queries for testing
	for i := range dataset.Queries {
		dataset.Queries[i].RelevantDocIDs = []string{
			"bnm-musharakah-001",
			"bnm-musharakah-002",
		}
	}

	ctx := context.Background()
	result, err := runner.Run(ctx, dataset)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify benchmark completed successfully
	assert.Equal(t, dataset.Name, result.DatasetName)
	assert.Equal(t, len(dataset.Queries), result.Metrics.TotalQueries)

	// Verify metrics are reasonable
	assert.GreaterOrEqual(t, result.Metrics.MRR, 0.0)
	assert.LessOrEqual(t, result.Metrics.MRR, 1.0)
	assert.GreaterOrEqual(t, result.Metrics.HitRateAt5, 0.0)
	assert.LessOrEqual(t, result.Metrics.HitRateAt5, 1.0)

	// Verify we have category breakdowns
	assert.NotEmpty(t, result.ByCategory)

	// Format and verify markdown output
	md := FormatResultsMarkdown(result)
	assert.NotEmpty(t, md)
	assert.Contains(t, md, dataset.Name)

	t.Logf("Benchmark completed:")
	t.Logf("  Total queries: %d", result.Metrics.TotalQueries)
	t.Logf("  MRR: %.4f", result.Metrics.MRR)
	t.Logf("  Hit Rate@5: %.4f", result.Metrics.HitRateAt5)
	t.Logf("  Categories evaluated: %d", len(result.ByCategory))
}
