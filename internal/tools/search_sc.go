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

// SearchSCInput represents the input parameters for the SC Malaysia search tool.
type SearchSCInput struct {
	Query     string     `json:"query"`
	Category  string     `json:"category,omitempty"`
	DateRange *DateRange `json:"date_range,omitempty"`
	TopK      int        `json:"top_k,omitempty"`
}

// SearchSCTool implements the search_sc_regulations tool.
type SearchSCTool struct {
	retriever *rag.Retriever
}

// NewSearchSCTool creates a new SC Malaysia search tool.
func NewSearchSCTool(retriever *rag.Retriever) *SearchSCTool {
	return &SearchSCTool{
		retriever: retriever,
	}
}

// Name returns the tool name.
func (t *SearchSCTool) Name() string {
	return "search_sc_regulations"
}

// Description returns the tool description.
func (t *SearchSCTool) Description() string {
	return `Search Securities Commission Malaysia (SC) regulations, guidelines, and Shariah Advisory Council resolutions for Islamic capital markets. Use this tool when the user asks about Malaysian Islamic capital market regulations, sukuk guidelines, SC Shariah requirements, or securities regulations.

Categories available:
- acts: Capital Markets and Services Act, Securities Commission Act, and related legislation
- guidelines: SC Islamic Capital Market Guidelines and regulatory frameworks
- shariah_resolutions: SC Shariah Advisory Council (SAC) Resolutions and rulings
- all: Search all categories (default)

Key topics covered:
- Sukuk (Islamic bonds) regulations
- Islamic fund management guidelines
- Shariah-compliant securities listing
- Islamic unit trust requirements
- Capital market Shariah compliance`
}

// InputSchema returns the JSON schema for the tool input.
func (t *SearchSCTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language search query for SC Malaysia regulations",
			},
			"category": map[string]any{
				"type":        "string",
				"enum":        []string{"acts", "guidelines", "shariah_resolutions", "all"},
				"description": "Category of documents to search. Default is 'all'.",
			},
			"date_range": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{
						"type":        "string",
						"format":      "date",
						"description": "Start date filter (ISO format: YYYY-MM-DD)",
					},
					"to": map[string]any{
						"type":        "string",
						"format":      "date",
						"description": "End date filter (ISO format: YYYY-MM-DD)",
					},
				},
				"description": "Optional date range filter for documents",
			},
			"top_k": map[string]any{
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

// Execute runs the SC Malaysia search tool.
func (t *SearchSCTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params SearchSCInput
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
		SourceType: "sc",
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
	return formatSCResults(result.Chunks, params.Query), nil
}

// formatSCResults formats the search results for the LLM.
func formatSCResults(chunks []storage.RetrievedChunk, query string) string {
	if len(chunks) == 0 {
		return fmt.Sprintf("No SC Malaysia regulations found for query: %q. Try rephrasing the search query or broadening the search criteria.", query)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant SC Malaysia documents:\n\n", len(chunks)))

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("[%d] ", i+1))

		// Document type and title
		docType := formatSCCategory(chunk.Category)
		if docType != "" {
			sb.WriteString(fmt.Sprintf("%s: ", docType))
		}
		sb.WriteString(chunk.DocumentTitle)
		sb.WriteString("\n")

		// Standard/Act number if available
		if chunk.StandardNum != "" {
			sb.WriteString(fmt.Sprintf("Reference: %s\n", chunk.StandardNum))
		}

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

// formatSCCategory converts internal category to display format.
func formatSCCategory(category string) string {
	switch category {
	case "acts":
		return "Act/Legislation"
	case "guidelines":
		return "Guidelines"
	case "shariah_resolutions":
		return "SAC Resolution"
	case "islamic-capital-market":
		return "Islamic Capital Market"
	default:
		if category != "" {
			return strings.Title(strings.ReplaceAll(category, "_", " "))
		}
		return ""
	}
}
