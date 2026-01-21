// Package evaluation provides RAG evaluation metrics and benchmarking tools.
package evaluation

import (
	"math"
	"sort"
)

// RetrievalResult represents a single retrieval result.
type RetrievalResult struct {
	ID         string  // Unique identifier for the retrieved item
	Score      float64 // Relevance score (higher is better)
	IsRelevant bool    // Ground truth: whether this result is relevant
}

// QueryResult represents the results for a single query.
type QueryResult struct {
	QueryID          string            // Unique identifier for the query
	Query            string            // The query text
	Results          []RetrievalResult // Retrieved results, ordered by relevance score
	RelevantIDs      []string          // Ground truth: IDs of all relevant documents
	LatencyMs        int64             // Retrieval latency in milliseconds
	TokensUsed       int               // Tokens used for embedding
}

// EvaluationMetrics contains all computed evaluation metrics.
type EvaluationMetrics struct {
	// Precision at K
	PrecisionAt1  float64 `json:"precision_at_1"`
	PrecisionAt3  float64 `json:"precision_at_3"`
	PrecisionAt5  float64 `json:"precision_at_5"`
	PrecisionAt10 float64 `json:"precision_at_10"`

	// Recall at K
	RecallAt1  float64 `json:"recall_at_1"`
	RecallAt3  float64 `json:"recall_at_3"`
	RecallAt5  float64 `json:"recall_at_5"`
	RecallAt10 float64 `json:"recall_at_10"`

	// Hit Rate (whether at least one relevant result was retrieved)
	HitRateAt1  float64 `json:"hit_rate_at_1"`
	HitRateAt3  float64 `json:"hit_rate_at_3"`
	HitRateAt5  float64 `json:"hit_rate_at_5"`
	HitRateAt10 float64 `json:"hit_rate_at_10"`

	// Mean Reciprocal Rank
	MRR float64 `json:"mrr"`

	// Normalized Discounted Cumulative Gain
	NDCGAt5  float64 `json:"ndcg_at_5"`
	NDCGAt10 float64 `json:"ndcg_at_10"`

	// Mean Average Precision
	MAP float64 `json:"map"`

	// Performance metrics
	MeanLatencyMs   float64 `json:"mean_latency_ms"`
	MedianLatencyMs float64 `json:"median_latency_ms"`
	P95LatencyMs    float64 `json:"p95_latency_ms"`
	P99LatencyMs    float64 `json:"p99_latency_ms"`

	// Query count
	TotalQueries     int `json:"total_queries"`
	QueriesWithHits  int `json:"queries_with_hits"`
}

// MetricsCalculator computes evaluation metrics from query results.
type MetricsCalculator struct{}

// NewMetricsCalculator creates a new metrics calculator.
func NewMetricsCalculator() *MetricsCalculator {
	return &MetricsCalculator{}
}

// Calculate computes all evaluation metrics from a set of query results.
func (mc *MetricsCalculator) Calculate(results []QueryResult) EvaluationMetrics {
	if len(results) == 0 {
		return EvaluationMetrics{}
	}

	metrics := EvaluationMetrics{
		TotalQueries: len(results),
	}

	var (
		precisionAt1Sum, precisionAt3Sum, precisionAt5Sum, precisionAt10Sum float64
		recallAt1Sum, recallAt3Sum, recallAt5Sum, recallAt10Sum             float64
		hitAt1Sum, hitAt3Sum, hitAt5Sum, hitAt10Sum                         float64
		mrrSum                                                              float64
		ndcgAt5Sum, ndcgAt10Sum                                             float64
		apSum                                                               float64
		latencies                                                           []float64
	)

	for _, qr := range results {
		// Build relevance set for quick lookup
		relevantSet := make(map[string]bool)
		for _, id := range qr.RelevantIDs {
			relevantSet[id] = true
		}

		// Precision at K
		precisionAt1Sum += mc.precisionAtK(qr.Results, relevantSet, 1)
		precisionAt3Sum += mc.precisionAtK(qr.Results, relevantSet, 3)
		precisionAt5Sum += mc.precisionAtK(qr.Results, relevantSet, 5)
		precisionAt10Sum += mc.precisionAtK(qr.Results, relevantSet, 10)

		// Recall at K
		if len(qr.RelevantIDs) > 0 {
			recallAt1Sum += mc.recallAtK(qr.Results, relevantSet, 1, len(qr.RelevantIDs))
			recallAt3Sum += mc.recallAtK(qr.Results, relevantSet, 3, len(qr.RelevantIDs))
			recallAt5Sum += mc.recallAtK(qr.Results, relevantSet, 5, len(qr.RelevantIDs))
			recallAt10Sum += mc.recallAtK(qr.Results, relevantSet, 10, len(qr.RelevantIDs))
		}

		// Hit Rate at K
		hitAt1 := mc.hitAtK(qr.Results, relevantSet, 1)
		hitAt3 := mc.hitAtK(qr.Results, relevantSet, 3)
		hitAt5 := mc.hitAtK(qr.Results, relevantSet, 5)
		hitAt10 := mc.hitAtK(qr.Results, relevantSet, 10)

		hitAt1Sum += hitAt1
		hitAt3Sum += hitAt3
		hitAt5Sum += hitAt5
		hitAt10Sum += hitAt10

		if hitAt1 > 0 || hitAt3 > 0 || hitAt5 > 0 || hitAt10 > 0 {
			metrics.QueriesWithHits++
		}

		// MRR
		mrrSum += mc.reciprocalRank(qr.Results, relevantSet)

		// NDCG
		ndcgAt5Sum += mc.ndcgAtK(qr.Results, relevantSet, 5)
		ndcgAt10Sum += mc.ndcgAtK(qr.Results, relevantSet, 10)

		// Average Precision
		apSum += mc.averagePrecision(qr.Results, relevantSet)

		// Latency
		latencies = append(latencies, float64(qr.LatencyMs))
	}

	n := float64(len(results))

	// Average metrics
	metrics.PrecisionAt1 = precisionAt1Sum / n
	metrics.PrecisionAt3 = precisionAt3Sum / n
	metrics.PrecisionAt5 = precisionAt5Sum / n
	metrics.PrecisionAt10 = precisionAt10Sum / n

	metrics.RecallAt1 = recallAt1Sum / n
	metrics.RecallAt3 = recallAt3Sum / n
	metrics.RecallAt5 = recallAt5Sum / n
	metrics.RecallAt10 = recallAt10Sum / n

	metrics.HitRateAt1 = hitAt1Sum / n
	metrics.HitRateAt3 = hitAt3Sum / n
	metrics.HitRateAt5 = hitAt5Sum / n
	metrics.HitRateAt10 = hitAt10Sum / n

	metrics.MRR = mrrSum / n
	metrics.NDCGAt5 = ndcgAt5Sum / n
	metrics.NDCGAt10 = ndcgAt10Sum / n
	metrics.MAP = apSum / n

	// Latency percentiles
	if len(latencies) > 0 {
		metrics.MeanLatencyMs = mc.mean(latencies)
		metrics.MedianLatencyMs = mc.percentile(latencies, 50)
		metrics.P95LatencyMs = mc.percentile(latencies, 95)
		metrics.P99LatencyMs = mc.percentile(latencies, 99)
	}

	return metrics
}

// precisionAtK calculates precision at K.
func (mc *MetricsCalculator) precisionAtK(results []RetrievalResult, relevant map[string]bool, k int) float64 {
	if k == 0 {
		return 0
	}

	count := 0
	limit := k
	if limit > len(results) {
		limit = len(results)
	}

	for i := 0; i < limit; i++ {
		if relevant[results[i].ID] {
			count++
		}
	}

	return float64(count) / float64(k)
}

// recallAtK calculates recall at K.
func (mc *MetricsCalculator) recallAtK(results []RetrievalResult, relevant map[string]bool, k, totalRelevant int) float64 {
	if totalRelevant == 0 {
		return 0
	}

	count := 0
	limit := k
	if limit > len(results) {
		limit = len(results)
	}

	for i := 0; i < limit; i++ {
		if relevant[results[i].ID] {
			count++
		}
	}

	return float64(count) / float64(totalRelevant)
}

// hitAtK returns 1.0 if there's at least one relevant result in top K, 0.0 otherwise.
func (mc *MetricsCalculator) hitAtK(results []RetrievalResult, relevant map[string]bool, k int) float64 {
	limit := k
	if limit > len(results) {
		limit = len(results)
	}

	for i := 0; i < limit; i++ {
		if relevant[results[i].ID] {
			return 1.0
		}
	}

	return 0.0
}

// reciprocalRank calculates the reciprocal rank (1/position of first relevant result).
func (mc *MetricsCalculator) reciprocalRank(results []RetrievalResult, relevant map[string]bool) float64 {
	for i, result := range results {
		if relevant[result.ID] {
			return 1.0 / float64(i+1)
		}
	}
	return 0.0
}

// ndcgAtK calculates Normalized Discounted Cumulative Gain at K.
func (mc *MetricsCalculator) ndcgAtK(results []RetrievalResult, relevant map[string]bool, k int) float64 {
	dcg := mc.dcgAtK(results, relevant, k)
	idcg := mc.idealDCGAtK(len(relevant), k)

	if idcg == 0 {
		return 0
	}

	return dcg / idcg
}

// dcgAtK calculates Discounted Cumulative Gain at K.
func (mc *MetricsCalculator) dcgAtK(results []RetrievalResult, relevant map[string]bool, k int) float64 {
	var dcg float64
	limit := k
	if limit > len(results) {
		limit = len(results)
	}

	for i := 0; i < limit; i++ {
		if relevant[results[i].ID] {
			// Binary relevance: 1 if relevant, 0 otherwise
			dcg += 1.0 / math.Log2(float64(i+2)) // +2 because position is 1-indexed and log2(1) = 0
		}
	}

	return dcg
}

// idealDCGAtK calculates the ideal DCG at K (all top K results are relevant).
func (mc *MetricsCalculator) idealDCGAtK(numRelevant, k int) float64 {
	var idcg float64
	limit := k
	if limit > numRelevant {
		limit = numRelevant
	}

	for i := 0; i < limit; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}

	return idcg
}

// averagePrecision calculates the average precision for a single query.
func (mc *MetricsCalculator) averagePrecision(results []RetrievalResult, relevant map[string]bool) float64 {
	if len(relevant) == 0 {
		return 0
	}

	var sum float64
	relevantCount := 0

	for i, result := range results {
		if relevant[result.ID] {
			relevantCount++
			// Precision at this point
			sum += float64(relevantCount) / float64(i+1)
		}
	}

	return sum / float64(len(relevant))
}

// mean calculates the mean of a slice of floats.
func (mc *MetricsCalculator) mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	var sum float64
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// percentile calculates the p-th percentile of a slice of floats.
func (mc *MetricsCalculator) percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	index := (p / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))

	if lower == upper {
		return sorted[lower]
	}

	// Linear interpolation
	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// ContextRelevanceMetrics measures how well the retrieved context supports the answer.
type ContextRelevanceMetrics struct {
	// Coverage measures what fraction of the answer is supported by context
	Coverage float64 `json:"coverage"`

	// Faithfulness measures how well the answer sticks to the context
	Faithfulness float64 `json:"faithfulness"`

	// Relevance measures how relevant the context is to the query
	Relevance float64 `json:"relevance"`
}

// AnswerQualityMetrics measures the quality of generated answers.
type AnswerQualityMetrics struct {
	// Correctness measures factual accuracy
	Correctness float64 `json:"correctness"`

	// Completeness measures if all aspects are covered
	Completeness float64 `json:"completeness"`

	// Coherence measures logical flow
	Coherence float64 `json:"coherence"`

	// Conciseness measures if the answer is appropriately brief
	Conciseness float64 `json:"conciseness"`
}

// FullEvaluationResult contains all evaluation metrics.
type FullEvaluationResult struct {
	RetrievalMetrics EvaluationMetrics       `json:"retrieval_metrics"`
	ContextMetrics   ContextRelevanceMetrics `json:"context_metrics"`
	AnswerMetrics    AnswerQualityMetrics    `json:"answer_metrics"`

	// Benchmark info
	BenchmarkName string `json:"benchmark_name"`
	Timestamp     string `json:"timestamp"`
	ConfigHash    string `json:"config_hash"`
}
