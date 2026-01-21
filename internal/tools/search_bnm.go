// Package tools provides agent tools for the ShariaComply AI system.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/rag"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
)

// SearchBNMInput represents the input parameters for the BNM search tool.
type SearchBNMInput struct {
	Query     string     `json:"query"`
	Category  string     `json:"category,omitempty"`
	DateRange *DateRange `json:"date_range,omitempty"`
	TopK      int        `json:"top_k,omitempty"`
}

// DateRange represents a date range filter.
type DateRange struct {
	From string `json:"from,omitempty"` // ISO date format
	To   string `json:"to,omitempty"`   // ISO date format
}

// SearchBNMTool implements the search_bnm_regulations tool.
type SearchBNMTool struct {
	retriever *rag.Retriever
}

// NewSearchBNMTool creates a new BNM search tool.
func NewSearchBNMTool(retriever *rag.Retriever) *SearchBNMTool {
	return &SearchBNMTool{
		retriever: retriever,
	}
}

// Name returns the tool name.
func (t *SearchBNMTool) Name() string {
	return "search_bnm_regulations"
}

// Description returns the tool description.
func (t *SearchBNMTool) Description() string {
	return `Search Bank Negara Malaysia (BNM) Islamic banking regulations, policy documents, and circulars. Use this tool when the user asks about Malaysian Islamic banking regulations, BNM requirements, or local regulatory compliance.

Categories available:
- policy_documents: Official BNM policy documents
- circulars: BNM circulars and announcements
- guidelines: BNM guidelines and frameworks
- all: Search all categories (default)`
}

// InputSchema returns the JSON schema for the tool input.
func (t *SearchBNMTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Natural language search query for BNM regulations",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"policy_documents", "circulars", "guidelines", "all"},
				"description": "Category of documents to search. Default is 'all'.",
			},
			"date_range": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from": map[string]interface{}{
						"type":        "string",
						"format":      "date",
						"description": "Start date filter (ISO format: YYYY-MM-DD)",
					},
					"to": map[string]interface{}{
						"type":        "string",
						"format":      "date",
						"description": "End date filter (ISO format: YYYY-MM-DD)",
					},
				},
				"description": "Optional date range filter for documents",
			},
			"top_k": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     10,
				"default":     5,
				"description": "Maximum number of results to return (1-10)",
			},
		},
		"required": []string{"query"},
	}
}

// Execute runs the BNM search tool.
func (t *SearchBNMTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params SearchBNMInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("failed to parse input: %w", err)
	}

	if params.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	// Set defaults
	topK := params.TopK
	if topK <= 0 {
		topK = 5
	}
	if topK > 10 {
		topK = 10
	}

	// Build retrieval options
	opts := rag.RetrievalOptions{
		TopK:       topK,
		SourceType: "bnm",
	}

	// Apply category filter
	if params.Category != "" && params.Category != "all" {
		opts.Categories = []string{params.Category}
	}

	// Apply date range filter
	if params.DateRange != nil {
		if params.DateRange.From != "" {
			fromDate, err := time.Parse("2006-01-02", params.DateRange.From)
			if err == nil {
				opts.DateRange.Start = fromDate
			}
		}
		if params.DateRange.To != "" {
			toDate, err := time.Parse("2006-01-02", params.DateRange.To)
			if err == nil {
				opts.DateRange.End = toDate
			}
		}
	}

	// Perform retrieval
	result, err := t.retriever.Retrieve(ctx, params.Query, opts)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	// Format results
	return formatBNMResults(result.Chunks, params.Query), nil
}

// formatBNMResults formats the search results for the LLM.
func formatBNMResults(chunks []storage.RetrievedChunk, query string) string {
	if len(chunks) == 0 {
		return fmt.Sprintf("No BNM regulations found for query: %q. Try rephrasing the search query or broadening the search criteria.", query)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant BNM documents:\n\n", len(chunks)))

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("[%d] ", i+1))

		// Document type and title
		if chunk.Category != "" {
			sb.WriteString(fmt.Sprintf("%s: ", formatCategory(chunk.Category)))
		}
		sb.WriteString(chunk.DocumentTitle)
		sb.WriteString("\n")

		// Section information
		if chunk.SectionTitle != "" {
			sb.WriteString(fmt.Sprintf("Section: %s\n", chunk.SectionTitle))
		}

		// Page information
		if chunk.PageNumber > 0 {
			sb.WriteString(fmt.Sprintf("Page: %d\n", chunk.PageNumber))
		}

		// Effective date
		if !chunk.EffectiveDate.IsZero() {
			sb.WriteString(fmt.Sprintf("Effective Date: %s\n", chunk.EffectiveDate.Format("2006-01-02")))
		}

		// Relevance score
		sb.WriteString(fmt.Sprintf("Relevance: %.2f%%\n", chunk.Similarity*100))

		// Content excerpt
		sb.WriteString(fmt.Sprintf("Content:\n\"%s\"\n", truncateContent(chunk.Content, 500)))

		sb.WriteString("\n")
	}

	return sb.String()
}

// formatCategory converts internal category to display format.
func formatCategory(category string) string {
	switch category {
	case "policy_documents":
		return "Policy Document"
	case "circulars":
		return "Circular"
	case "guidelines":
		return "Guidelines"
	default:
		return strings.Title(strings.ReplaceAll(category, "_", " "))
	}
}

// truncateContent truncates content to a maximum length while preserving words.
func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// Find the last space before maxLen
	truncated := content[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen-50 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}
