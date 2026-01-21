// Package evaluation provides RAG evaluation metrics and benchmarking tools.
package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// BenchmarkQuery represents a single benchmark query with ground truth.
type BenchmarkQuery struct {
	ID            string   `json:"id"`
	Query         string   `json:"query"`
	Category      string   `json:"category"`
	RelevantDocIDs []string `json:"relevant_doc_ids"`
	ExpectedAnswer string   `json:"expected_answer,omitempty"`
	Keywords      []string `json:"keywords,omitempty"`
}

// BenchmarkDataset represents a collection of benchmark queries.
type BenchmarkDataset struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Version     string           `json:"version"`
	Queries     []BenchmarkQuery `json:"queries"`
	Categories  []string         `json:"categories"`
}

// Retriever defines the interface for retrieval systems to be benchmarked.
type Retriever interface {
	// Retrieve performs retrieval for a given query.
	Retrieve(ctx context.Context, query string, topK int) ([]RetrievalResult, error)
}

// BenchmarkConfig holds configuration for running benchmarks.
type BenchmarkConfig struct {
	// TopK values to evaluate
	TopKValues []int

	// Number of parallel workers
	Workers int

	// Timeout per query
	QueryTimeout time.Duration

	// Whether to include latency metrics
	MeasureLatency bool

	// Output format
	OutputFormat string // "json", "csv", "markdown"
}

// DefaultBenchmarkConfig returns a default benchmark configuration.
func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		TopKValues:     []int{1, 3, 5, 10},
		Workers:        4,
		QueryTimeout:   30 * time.Second,
		MeasureLatency: true,
		OutputFormat:   "json",
	}
}

// BenchmarkRunner executes benchmarks against a retriever.
type BenchmarkRunner struct {
	retriever  Retriever
	calculator *MetricsCalculator
	config     BenchmarkConfig
	logger     *slog.Logger
}

// NewBenchmarkRunner creates a new benchmark runner.
func NewBenchmarkRunner(retriever Retriever, config BenchmarkConfig, logger *slog.Logger) *BenchmarkRunner {
	if logger == nil {
		logger = slog.Default()
	}

	return &BenchmarkRunner{
		retriever:  retriever,
		calculator: NewMetricsCalculator(),
		config:     config,
		logger:     logger.With("component", "benchmark"),
	}
}

// BenchmarkResult contains results from running a benchmark.
type BenchmarkResult struct {
	DatasetName string             `json:"dataset_name"`
	Timestamp   time.Time          `json:"timestamp"`
	Config      BenchmarkConfig    `json:"config"`
	Metrics     EvaluationMetrics  `json:"metrics"`
	ByCategory  map[string]EvaluationMetrics `json:"by_category"`
	QueryResults []QueryResult     `json:"query_results,omitempty"`
	Errors      []BenchmarkError   `json:"errors,omitempty"`
}

// BenchmarkError represents an error that occurred during benchmarking.
type BenchmarkError struct {
	QueryID string `json:"query_id"`
	Error   string `json:"error"`
}

// Run executes the benchmark against the given dataset.
func (br *BenchmarkRunner) Run(ctx context.Context, dataset BenchmarkDataset) (*BenchmarkResult, error) {
	br.logger.Info("starting benchmark",
		"dataset", dataset.Name,
		"queries", len(dataset.Queries),
	)

	result := &BenchmarkResult{
		DatasetName:  dataset.Name,
		Timestamp:    time.Now(),
		Config:       br.config,
		ByCategory:   make(map[string]EvaluationMetrics),
		QueryResults: make([]QueryResult, 0, len(dataset.Queries)),
	}

	// Use the maximum TopK for retrieval
	maxK := 10
	for _, k := range br.config.TopKValues {
		if k > maxK {
			maxK = k
		}
	}

	// Run queries
	categoryResults := make(map[string][]QueryResult)

	for _, bq := range dataset.Queries {
		queryCtx, cancel := context.WithTimeout(ctx, br.config.QueryTimeout)

		start := time.Now()
		results, err := br.retriever.Retrieve(queryCtx, bq.Query, maxK)
		latency := time.Since(start).Milliseconds()

		cancel()

		if err != nil {
			br.logger.Warn("query failed", "query_id", bq.ID, "error", err)
			result.Errors = append(result.Errors, BenchmarkError{
				QueryID: bq.ID,
				Error:   err.Error(),
			})
			continue
		}

		qr := QueryResult{
			QueryID:     bq.ID,
			Query:       bq.Query,
			Results:     results,
			RelevantIDs: bq.RelevantDocIDs,
			LatencyMs:   latency,
		}

		result.QueryResults = append(result.QueryResults, qr)

		// Group by category
		if bq.Category != "" {
			categoryResults[bq.Category] = append(categoryResults[bq.Category], qr)
		}
	}

	// Calculate overall metrics
	result.Metrics = br.calculator.Calculate(result.QueryResults)

	// Calculate per-category metrics
	for category, qrs := range categoryResults {
		result.ByCategory[category] = br.calculator.Calculate(qrs)
	}

	br.logger.Info("benchmark completed",
		"total_queries", len(dataset.Queries),
		"successful_queries", len(result.QueryResults),
		"errors", len(result.Errors),
		"mrr", result.Metrics.MRR,
		"hit_rate_at_5", result.Metrics.HitRateAt5,
	)

	return result, nil
}

// RunComparison runs the benchmark against multiple retrievers and compares results.
func (br *BenchmarkRunner) RunComparison(ctx context.Context, dataset BenchmarkDataset, retrievers map[string]Retriever) (map[string]*BenchmarkResult, error) {
	results := make(map[string]*BenchmarkResult)

	for name, retriever := range retrievers {
		br.logger.Info("running comparison benchmark", "retriever", name)

		runner := NewBenchmarkRunner(retriever, br.config, br.logger)
		result, err := runner.Run(ctx, dataset)
		if err != nil {
			return nil, fmt.Errorf("benchmark failed for %s: %w", name, err)
		}

		results[name] = result
	}

	return results, nil
}

// SaveResults saves benchmark results to a file.
func (br *BenchmarkRunner) SaveResults(result *BenchmarkResult, filepath string) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal results: %w", err)
	}

	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return fmt.Errorf("failed to write results: %w", err)
	}

	return nil
}

// LoadDataset loads a benchmark dataset from a JSON file.
func LoadDataset(filepath string) (*BenchmarkDataset, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read dataset: %w", err)
	}

	var dataset BenchmarkDataset
	if err := json.Unmarshal(data, &dataset); err != nil {
		return nil, fmt.Errorf("failed to parse dataset: %w", err)
	}

	return &dataset, nil
}

// CreateIslamicBankingBenchmark creates a sample benchmark dataset for Islamic banking.
func CreateIslamicBankingBenchmark() BenchmarkDataset {
	return BenchmarkDataset{
		Name:        "islamic-banking-benchmark-v1",
		Description: "Benchmark dataset for Islamic banking compliance questions",
		Version:     "1.0.0",
		Categories: []string{
			"musharakah",
			"murabaha",
			"ijara",
			"sukuk",
			"takaful",
			"general_shariah",
			"bnm_regulations",
			"aaoifi_standards",
		},
		Queries: []BenchmarkQuery{
			// Musharakah queries
			{
				ID:       "mush-001",
				Query:    "What are the key requirements for a valid musharakah contract?",
				Category: "musharakah",
				Keywords: []string{"musharakah", "partnership", "profit sharing", "loss sharing"},
			},
			{
				ID:       "mush-002",
				Query:    "How should profits be distributed in diminishing musharakah?",
				Category: "musharakah",
				Keywords: []string{"diminishing musharakah", "profit distribution", "ownership transfer"},
			},
			{
				ID:       "mush-003",
				Query:    "What is the difference between musharakah and mudarabah?",
				Category: "musharakah",
				Keywords: []string{"musharakah", "mudarabah", "capital contribution", "management"},
			},

			// Murabaha queries
			{
				ID:       "mura-001",
				Query:    "What disclosures are required in a murabaha transaction?",
				Category: "murabaha",
				Keywords: []string{"murabaha", "disclosure", "cost plus", "markup"},
			},
			{
				ID:       "mura-002",
				Query:    "Can the price be changed after a murabaha contract is signed?",
				Category: "murabaha",
				Keywords: []string{"murabaha", "price", "contract", "amendment"},
			},
			{
				ID:       "mura-003",
				Query:    "What are the shariah requirements for commodity murabaha?",
				Category: "murabaha",
				Keywords: []string{"commodity murabaha", "tawarruq", "organized", "shariah"},
			},

			// Ijara queries
			{
				ID:       "ijar-001",
				Query:    "What are the maintenance obligations in ijara contracts?",
				Category: "ijara",
				Keywords: []string{"ijara", "maintenance", "lessor", "lessee"},
			},
			{
				ID:       "ijar-002",
				Query:    "How does ijara muntahia bittamlik work?",
				Category: "ijara",
				Keywords: []string{"ijara", "muntahia bittamlik", "ownership transfer", "lease to own"},
			},
			{
				ID:       "ijar-003",
				Query:    "What happens if the leased asset is destroyed in ijara?",
				Category: "ijara",
				Keywords: []string{"ijara", "asset destruction", "risk", "liability"},
			},

			// Sukuk queries
			{
				ID:       "sukuk-001",
				Query:    "What are the shariah requirements for sukuk issuance?",
				Category: "sukuk",
				Keywords: []string{"sukuk", "issuance", "underlying asset", "shariah"},
			},
			{
				ID:       "sukuk-002",
				Query:    "How should sukuk returns be structured to be shariah compliant?",
				Category: "sukuk",
				Keywords: []string{"sukuk", "returns", "profit", "shariah compliant"},
			},
			{
				ID:       "sukuk-003",
				Query:    "What is the difference between asset-backed and asset-based sukuk?",
				Category: "sukuk",
				Keywords: []string{"sukuk", "asset-backed", "asset-based", "ownership"},
			},

			// Takaful queries
			{
				ID:       "taka-001",
				Query:    "What are the models for takaful operations?",
				Category: "takaful",
				Keywords: []string{"takaful", "wakalah", "mudarabah", "hybrid"},
			},
			{
				ID:       "taka-002",
				Query:    "How should takaful surplus be distributed?",
				Category: "takaful",
				Keywords: []string{"takaful", "surplus", "distribution", "participants"},
			},

			// General Shariah queries
			{
				ID:       "gen-001",
				Query:    "What constitutes riba in Islamic finance?",
				Category: "general_shariah",
				Keywords: []string{"riba", "interest", "prohibited", "shariah"},
			},
			{
				ID:       "gen-002",
				Query:    "What are the conditions for a valid sale contract in shariah?",
				Category: "general_shariah",
				Keywords: []string{"sale", "contract", "conditions", "shariah"},
			},
			{
				ID:       "gen-003",
				Query:    "What is gharar and how does it affect contracts?",
				Category: "general_shariah",
				Keywords: []string{"gharar", "uncertainty", "prohibited", "contracts"},
			},

			// BNM regulation queries
			{
				ID:       "bnm-001",
				Query:    "What are BNM's capital requirements for Islamic banks?",
				Category: "bnm_regulations",
				Keywords: []string{"bnm", "capital", "requirements", "islamic banks"},
			},
			{
				ID:       "bnm-002",
				Query:    "What are BNM's guidelines on shariah governance?",
				Category: "bnm_regulations",
				Keywords: []string{"bnm", "shariah governance", "guidelines", "committee"},
			},

			// AAOIFI standard queries
			{
				ID:       "aaoifi-001",
				Query:    "What does AAOIFI standard 21 say about financial papers?",
				Category: "aaoifi_standards",
				Keywords: []string{"aaoifi", "standard 21", "financial papers"},
			},
			{
				ID:       "aaoifi-002",
				Query:    "What are AAOIFI's requirements for zakat calculation?",
				Category: "aaoifi_standards",
				Keywords: []string{"aaoifi", "zakat", "calculation", "requirements"},
			},
		},
	}
}

// FormatResultsMarkdown formats benchmark results as markdown.
func FormatResultsMarkdown(result *BenchmarkResult) string {
	md := fmt.Sprintf("# Benchmark Results: %s\n\n", result.DatasetName)
	md += fmt.Sprintf("**Date:** %s\n\n", result.Timestamp.Format(time.RFC3339))

	md += "## Overall Metrics\n\n"
	md += "| Metric | Value |\n"
	md += "|--------|-------|\n"
	md += fmt.Sprintf("| MRR | %.4f |\n", result.Metrics.MRR)
	md += fmt.Sprintf("| MAP | %.4f |\n", result.Metrics.MAP)
	md += fmt.Sprintf("| NDCG@5 | %.4f |\n", result.Metrics.NDCGAt5)
	md += fmt.Sprintf("| NDCG@10 | %.4f |\n", result.Metrics.NDCGAt10)
	md += "\n"

	md += "### Precision\n\n"
	md += "| K | Precision |\n"
	md += "|---|----------|\n"
	md += fmt.Sprintf("| 1 | %.4f |\n", result.Metrics.PrecisionAt1)
	md += fmt.Sprintf("| 3 | %.4f |\n", result.Metrics.PrecisionAt3)
	md += fmt.Sprintf("| 5 | %.4f |\n", result.Metrics.PrecisionAt5)
	md += fmt.Sprintf("| 10 | %.4f |\n", result.Metrics.PrecisionAt10)
	md += "\n"

	md += "### Recall\n\n"
	md += "| K | Recall |\n"
	md += "|---|--------|\n"
	md += fmt.Sprintf("| 1 | %.4f |\n", result.Metrics.RecallAt1)
	md += fmt.Sprintf("| 3 | %.4f |\n", result.Metrics.RecallAt3)
	md += fmt.Sprintf("| 5 | %.4f |\n", result.Metrics.RecallAt5)
	md += fmt.Sprintf("| 10 | %.4f |\n", result.Metrics.RecallAt10)
	md += "\n"

	md += "### Hit Rate\n\n"
	md += "| K | Hit Rate |\n"
	md += "|---|----------|\n"
	md += fmt.Sprintf("| 1 | %.4f |\n", result.Metrics.HitRateAt1)
	md += fmt.Sprintf("| 3 | %.4f |\n", result.Metrics.HitRateAt3)
	md += fmt.Sprintf("| 5 | %.4f |\n", result.Metrics.HitRateAt5)
	md += fmt.Sprintf("| 10 | %.4f |\n", result.Metrics.HitRateAt10)
	md += "\n"

	md += "### Latency\n\n"
	md += "| Metric | Value (ms) |\n"
	md += "|--------|------------|\n"
	md += fmt.Sprintf("| Mean | %.2f |\n", result.Metrics.MeanLatencyMs)
	md += fmt.Sprintf("| Median | %.2f |\n", result.Metrics.MedianLatencyMs)
	md += fmt.Sprintf("| P95 | %.2f |\n", result.Metrics.P95LatencyMs)
	md += fmt.Sprintf("| P99 | %.2f |\n", result.Metrics.P99LatencyMs)
	md += "\n"

	if len(result.ByCategory) > 0 {
		md += "## Results by Category\n\n"
		md += "| Category | MRR | Precision@5 | Recall@5 | Hit Rate@5 |\n"
		md += "|----------|-----|-------------|----------|------------|\n"
		for category, metrics := range result.ByCategory {
			md += fmt.Sprintf("| %s | %.4f | %.4f | %.4f | %.4f |\n",
				category, metrics.MRR, metrics.PrecisionAt5, metrics.RecallAt5, metrics.HitRateAt5)
		}
		md += "\n"
	}

	if len(result.Errors) > 0 {
		md += "## Errors\n\n"
		md += fmt.Sprintf("Total errors: %d\n\n", len(result.Errors))
		for _, err := range result.Errors {
			md += fmt.Sprintf("- **%s**: %s\n", err.QueryID, err.Error)
		}
	}

	return md
}
