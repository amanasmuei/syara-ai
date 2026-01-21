// Package evaluation provides RAG evaluation metrics and benchmarking tools.
package evaluation

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsCalculator_PrecisionAtK(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name     string
		results  []RetrievalResult
		relevant map[string]bool
		k        int
		expected float64
	}{
		{
			name: "all relevant at k=3",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
				{ID: "c", Score: 0.7},
			},
			relevant: map[string]bool{"a": true, "b": true, "c": true},
			k:        3,
			expected: 1.0,
		},
		{
			name: "half relevant at k=4",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
				{ID: "c", Score: 0.7},
				{ID: "d", Score: 0.6},
			},
			relevant: map[string]bool{"a": true, "c": true},
			k:        4,
			expected: 0.5,
		},
		{
			name: "none relevant",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
			},
			relevant: map[string]bool{"x": true, "y": true},
			k:        2,
			expected: 0.0,
		},
		{
			name: "k larger than results",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
			},
			relevant: map[string]bool{"a": true, "b": true},
			k:        5,
			expected: 2.0 / 5.0, // 2 relevant out of k=5
		},
		{
			name:     "empty results",
			results:  []RetrievalResult{},
			relevant: map[string]bool{"a": true},
			k:        3,
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.precisionAtK(tt.results, tt.relevant, tt.k)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestMetricsCalculator_RecallAtK(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name          string
		results       []RetrievalResult
		relevant      map[string]bool
		k             int
		totalRelevant int
		expected      float64
	}{
		{
			name: "all relevant found at k=3",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
				{ID: "c", Score: 0.7},
			},
			relevant:      map[string]bool{"a": true, "b": true},
			k:             3,
			totalRelevant: 2,
			expected:      1.0,
		},
		{
			name: "half relevant found",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "x", Score: 0.8},
				{ID: "y", Score: 0.7},
			},
			relevant:      map[string]bool{"a": true, "b": true},
			k:             3,
			totalRelevant: 2,
			expected:      0.5,
		},
		{
			name: "no relevant found",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "y", Score: 0.8},
			},
			relevant:      map[string]bool{"a": true, "b": true},
			k:             2,
			totalRelevant: 2,
			expected:      0.0,
		},
		{
			name:          "zero total relevant",
			results:       []RetrievalResult{{ID: "a", Score: 0.9}},
			relevant:      map[string]bool{},
			k:             1,
			totalRelevant: 0,
			expected:      0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.recallAtK(tt.results, tt.relevant, tt.k, tt.totalRelevant)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestMetricsCalculator_HitAtK(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name     string
		results  []RetrievalResult
		relevant map[string]bool
		k        int
		expected float64
	}{
		{
			name: "hit at position 1",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
			},
			relevant: map[string]bool{"a": true},
			k:        3,
			expected: 1.0,
		},
		{
			name: "hit at position 2",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "a", Score: 0.8},
			},
			relevant: map[string]bool{"a": true},
			k:        3,
			expected: 1.0,
		},
		{
			name: "no hit within k",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "y", Score: 0.8},
				{ID: "a", Score: 0.7},
			},
			relevant: map[string]bool{"a": true},
			k:        2,
			expected: 0.0,
		},
		{
			name:     "empty results",
			results:  []RetrievalResult{},
			relevant: map[string]bool{"a": true},
			k:        3,
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.hitAtK(tt.results, tt.relevant, tt.k)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMetricsCalculator_ReciprocalRank(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name     string
		results  []RetrievalResult
		relevant map[string]bool
		expected float64
	}{
		{
			name: "relevant at position 1",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
			},
			relevant: map[string]bool{"a": true},
			expected: 1.0,
		},
		{
			name: "relevant at position 2",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "a", Score: 0.8},
			},
			relevant: map[string]bool{"a": true},
			expected: 0.5,
		},
		{
			name: "relevant at position 3",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "y", Score: 0.8},
				{ID: "a", Score: 0.7},
			},
			relevant: map[string]bool{"a": true},
			expected: 1.0 / 3.0,
		},
		{
			name: "no relevant found",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "y", Score: 0.8},
			},
			relevant: map[string]bool{"a": true},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.reciprocalRank(tt.results, tt.relevant)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestMetricsCalculator_NDCG(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name     string
		results  []RetrievalResult
		relevant map[string]bool
		k        int
		expected float64
	}{
		{
			name: "perfect ranking",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
			},
			relevant: map[string]bool{"a": true, "b": true},
			k:        2,
			expected: 1.0, // Perfect ranking should give NDCG = 1
		},
		{
			name: "reversed ranking",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "a", Score: 0.8},
			},
			relevant: map[string]bool{"a": true},
			k:        2,
			// DCG = 0 + 1/log2(3) = 0.631
			// IDCG = 1/log2(2) = 1
			// NDCG = 0.631
			expected: 1.0 / math.Log2(3),
		},
		{
			name:     "no relevant in results",
			results:  []RetrievalResult{{ID: "x", Score: 0.9}},
			relevant: map[string]bool{"a": true},
			k:        1,
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.ndcgAtK(tt.results, tt.relevant, tt.k)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestMetricsCalculator_AveragePrecision(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name     string
		results  []RetrievalResult
		relevant map[string]bool
		expected float64
	}{
		{
			name: "all relevant at top",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
				{ID: "x", Score: 0.7},
			},
			relevant: map[string]bool{"a": true, "b": true},
			// AP = (1/1 + 2/2) / 2 = 1.0
			expected: 1.0,
		},
		{
			name: "interleaved relevant",
			results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "x", Score: 0.8},
				{ID: "b", Score: 0.7},
			},
			relevant: map[string]bool{"a": true, "b": true},
			// AP = (1/1 + 2/3) / 2 = 0.833
			expected: (1.0 + 2.0/3.0) / 2.0,
		},
		{
			name: "relevant at end",
			results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "y", Score: 0.8},
				{ID: "a", Score: 0.7},
			},
			relevant: map[string]bool{"a": true},
			// AP = (1/3) / 1 = 0.333
			expected: 1.0 / 3.0,
		},
		{
			name:     "no relevant in results",
			results:  []RetrievalResult{{ID: "x", Score: 0.9}},
			relevant: map[string]bool{"a": true},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.averagePrecision(tt.results, tt.relevant)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestMetricsCalculator_Calculate(t *testing.T) {
	mc := NewMetricsCalculator()

	// Create test query results
	queryResults := []QueryResult{
		{
			QueryID: "q1",
			Query:   "test query 1",
			Results: []RetrievalResult{
				{ID: "a", Score: 0.9},
				{ID: "b", Score: 0.8},
				{ID: "c", Score: 0.7},
			},
			RelevantIDs: []string{"a", "b"},
			LatencyMs:   100,
		},
		{
			QueryID: "q2",
			Query:   "test query 2",
			Results: []RetrievalResult{
				{ID: "x", Score: 0.9},
				{ID: "a", Score: 0.8},
				{ID: "b", Score: 0.7},
			},
			RelevantIDs: []string{"a", "b"},
			LatencyMs:   150,
		},
	}

	metrics := mc.Calculate(queryResults)

	// Verify basic metrics are calculated
	assert.Equal(t, 2, metrics.TotalQueries)
	assert.Equal(t, 2, metrics.QueriesWithHits)

	// Verify precision metrics are in valid range
	assert.GreaterOrEqual(t, metrics.PrecisionAt1, 0.0)
	assert.LessOrEqual(t, metrics.PrecisionAt1, 1.0)

	// Verify MRR is calculated
	// q1: first relevant at position 1 -> RR = 1
	// q2: first relevant at position 2 -> RR = 0.5
	// MRR = (1 + 0.5) / 2 = 0.75
	assert.InDelta(t, 0.75, metrics.MRR, 0.0001)

	// Verify latency metrics
	assert.Equal(t, 125.0, metrics.MeanLatencyMs) // (100 + 150) / 2
}

func TestMetricsCalculator_Calculate_EmptyResults(t *testing.T) {
	mc := NewMetricsCalculator()

	metrics := mc.Calculate([]QueryResult{})

	assert.Equal(t, 0, metrics.TotalQueries)
	assert.Equal(t, 0.0, metrics.MRR)
	assert.Equal(t, 0.0, metrics.MAP)
}

func TestMetricsCalculator_Percentile(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name       string
		values     []float64
		percentile float64
		expected   float64
	}{
		{
			name:       "median of odd count",
			values:     []float64{1, 2, 3, 4, 5},
			percentile: 50,
			expected:   3.0,
		},
		{
			name:       "median of even count",
			values:     []float64{1, 2, 3, 4},
			percentile: 50,
			expected:   2.5,
		},
		{
			name:       "p95",
			values:     []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			percentile: 95,
			expected:   9.55, // Linear interpolation
		},
		{
			name:       "p0 (min)",
			values:     []float64{1, 2, 3, 4, 5},
			percentile: 0,
			expected:   1.0,
		},
		{
			name:       "p100 (max)",
			values:     []float64{1, 2, 3, 4, 5},
			percentile: 100,
			expected:   5.0,
		},
		{
			name:       "empty values",
			values:     []float64{},
			percentile: 50,
			expected:   0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.percentile(tt.values, tt.percentile)
			assert.InDelta(t, tt.expected, result, 0.01)
		})
	}
}

func TestMetricsCalculator_Mean(t *testing.T) {
	mc := NewMetricsCalculator()

	tests := []struct {
		name     string
		values   []float64
		expected float64
	}{
		{
			name:     "simple mean",
			values:   []float64{1, 2, 3, 4, 5},
			expected: 3.0,
		},
		{
			name:     "single value",
			values:   []float64{42},
			expected: 42.0,
		},
		{
			name:     "empty values",
			values:   []float64{},
			expected: 0.0,
		},
		{
			name:     "negative values",
			values:   []float64{-1, 0, 1},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mc.mean(tt.values)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

// Benchmark tests
func BenchmarkMetricsCalculator_Calculate(b *testing.B) {
	mc := NewMetricsCalculator()

	// Create realistic query results
	queryResults := make([]QueryResult, 100)
	for i := range queryResults {
		results := make([]RetrievalResult, 10)
		for j := range results {
			results[j] = RetrievalResult{
				ID:    string(rune('a' + j)),
				Score: float64(10-j) / 10.0,
			}
		}

		queryResults[i] = QueryResult{
			QueryID:     string(rune('0' + i)),
			Results:     results,
			RelevantIDs: []string{"a", "b", "c"},
			LatencyMs:   int64(100 + i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.Calculate(queryResults)
	}
}

func BenchmarkMetricsCalculator_NDCG(b *testing.B) {
	mc := NewMetricsCalculator()

	results := make([]RetrievalResult, 100)
	for i := range results {
		results[i] = RetrievalResult{
			ID:    string(rune('a' + i%26)),
			Score: float64(100-i) / 100.0,
		}
	}

	relevant := make(map[string]bool)
	for i := 0; i < 10; i++ {
		relevant[string(rune('a'+i))] = true
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.ndcgAtK(results, relevant, 10)
	}
}

// Test the full evaluation flow
func TestFullEvaluation(t *testing.T) {
	mc := NewMetricsCalculator()

	// Create a realistic evaluation scenario
	queryResults := []QueryResult{
		{
			QueryID: "musharakah-001",
			Query:   "What are the requirements for musharakah?",
			Results: []RetrievalResult{
				{ID: "doc1-chunk1", Score: 0.95},
				{ID: "doc2-chunk3", Score: 0.85},
				{ID: "doc1-chunk2", Score: 0.80},
				{ID: "doc3-chunk1", Score: 0.75},
				{ID: "doc4-chunk2", Score: 0.70},
			},
			RelevantIDs: []string{"doc1-chunk1", "doc1-chunk2", "doc2-chunk3"},
			LatencyMs:   250,
		},
		{
			QueryID: "murabaha-001",
			Query:   "What disclosures are needed for murabaha?",
			Results: []RetrievalResult{
				{ID: "doc5-chunk1", Score: 0.90},
				{ID: "doc6-chunk2", Score: 0.85},
				{ID: "doc5-chunk2", Score: 0.80},
				{ID: "doc7-chunk1", Score: 0.75},
				{ID: "doc8-chunk1", Score: 0.70},
			},
			RelevantIDs: []string{"doc5-chunk1", "doc5-chunk2"},
			LatencyMs:   180,
		},
		{
			QueryID: "sukuk-001",
			Query:   "What are sukuk requirements?",
			Results: []RetrievalResult{
				{ID: "doc9-chunk1", Score: 0.88},
				{ID: "doc10-chunk1", Score: 0.82},
				{ID: "doc9-chunk2", Score: 0.78},
				{ID: "doc11-chunk1", Score: 0.75},
				{ID: "doc12-chunk1", Score: 0.70},
			},
			RelevantIDs: []string{"doc9-chunk1", "doc9-chunk2", "doc10-chunk1"},
			LatencyMs:   200,
		},
	}

	metrics := mc.Calculate(queryResults)

	// Verify all metrics are calculated
	require.Equal(t, 3, metrics.TotalQueries)
	require.Equal(t, 3, metrics.QueriesWithHits)

	// All queries have hits at position 1
	assert.Equal(t, 1.0, metrics.HitRateAt1)

	// Check MRR (all first results are relevant)
	assert.Equal(t, 1.0, metrics.MRR)

	// Precision@1 should be 1.0 (all first results are relevant)
	assert.Equal(t, 1.0, metrics.PrecisionAt1)

	// Verify latency
	assert.InDelta(t, 210.0, metrics.MeanLatencyMs, 0.1) // (250 + 180 + 200) / 3

	// Log metrics for debugging
	t.Logf("Full Evaluation Metrics:")
	t.Logf("  MRR: %.4f", metrics.MRR)
	t.Logf("  MAP: %.4f", metrics.MAP)
	t.Logf("  NDCG@5: %.4f", metrics.NDCGAt5)
	t.Logf("  Precision@5: %.4f", metrics.PrecisionAt5)
	t.Logf("  Recall@5: %.4f", metrics.RecallAt5)
	t.Logf("  Hit Rate@5: %.4f", metrics.HitRateAt5)
	t.Logf("  Mean Latency: %.2f ms", metrics.MeanLatencyMs)
}
