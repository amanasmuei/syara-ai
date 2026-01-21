// Package tools provides agent tools for the ShariaComply AI system.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alqutdigital/islamic-banking-agent/internal/rag"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
)

// SearchAAOIFIInput represents the input parameters for the AAOIFI search tool.
type SearchAAOIFIInput struct {
	Query          string `json:"query"`
	StandardNumber string `json:"standard_number,omitempty"`
	Topic          string `json:"topic,omitempty"`
	TopK           int    `json:"top_k,omitempty"`
}

// SearchAAOIFITool implements the search_aaoifi_standards tool.
type SearchAAOIFITool struct {
	retriever *rag.Retriever
}

// NewSearchAAOIFITool creates a new AAOIFI search tool.
func NewSearchAAOIFITool(retriever *rag.Retriever) *SearchAAOIFITool {
	return &SearchAAOIFITool{
		retriever: retriever,
	}
}

// Name returns the tool name.
func (t *SearchAAOIFITool) Name() string {
	return "search_aaoifi_standards"
}

// Description returns the tool description.
func (t *SearchAAOIFITool) Description() string {
	return `Search AAOIFI (Accounting and Auditing Organization for Islamic Financial Institutions) Shariah Standards. Use this tool for international Islamic finance standards, scholarly rulings, and global best practices.

Key AAOIFI Shariah Standards:
- SS 1: Trading in Currencies
- SS 8: Murabaha (Cost-Plus Sale)
- SS 9: Ijarah (Leasing)
- SS 10: Salam (Forward Sale)
- SS 11: Istisna'a (Manufacturing Contract)
- SS 12: Sharikah (Partnership/Musharakah)
- SS 17: Investment Sukuk
- SS 26: Islamic Insurance (Takaful)

Topics available:
- murabaha: Trade-based financing
- musharakah: Partnership financing
- ijarah: Leasing contracts
- sukuk: Islamic bonds
- takaful: Islamic insurance
- general: General Shariah principles`
}

// InputSchema returns the JSON schema for the tool input.
func (t *SearchAAOIFITool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Natural language search query for AAOIFI standards",
			},
			"standard_number": map[string]interface{}{
				"type":        "string",
				"description": "Specific AAOIFI Shariah Standard number (e.g., 'SS 8' for Murabaha, 'SS 12' for Musharakah)",
			},
			"topic": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"murabaha", "musharakah", "ijarah", "sukuk", "takaful", "general"},
				"description": "Topic area filter for focused search",
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

// Execute runs the AAOIFI search tool.
func (t *SearchAAOIFITool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params SearchAAOIFIInput
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
		SourceType: "aaoifi",
	}

	// Apply standard number filter
	if params.StandardNumber != "" {
		opts.StandardNumber = normalizeStandardNumber(params.StandardNumber)
	}

	// Apply topic filter as category
	if params.Topic != "" && params.Topic != "general" {
		opts.Categories = []string{params.Topic}
	}

	// Enhance query with topic context if specified
	query := params.Query
	if params.Topic != "" && params.Topic != "general" {
		query = enhanceQueryWithTopic(query, params.Topic)
	}

	// Perform retrieval
	result, err := t.retriever.Retrieve(ctx, query, opts)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	// Format results
	return formatAAOIFIResults(result.Chunks, params.Query), nil
}

// normalizeStandardNumber normalizes different formats of standard numbers.
func normalizeStandardNumber(stdNum string) string {
	// Handle various formats: "SS 8", "SS8", "8", "Shariah Standard 8"
	stdNum = strings.TrimSpace(strings.ToUpper(stdNum))
	stdNum = strings.ReplaceAll(stdNum, "SHARIAH STANDARD", "SS")
	stdNum = strings.ReplaceAll(stdNum, "SHARIAH", "SS")
	stdNum = strings.TrimPrefix(stdNum, "NO.")
	stdNum = strings.TrimPrefix(stdNum, "NO")
	stdNum = strings.TrimSpace(stdNum)

	// Ensure SS prefix
	if !strings.HasPrefix(stdNum, "SS") {
		stdNum = "SS " + stdNum
	}

	// Normalize spacing
	stdNum = strings.ReplaceAll(stdNum, "SS", "SS ")
	for strings.Contains(stdNum, "  ") {
		stdNum = strings.ReplaceAll(stdNum, "  ", " ")
	}

	return strings.TrimSpace(stdNum)
}

// enhanceQueryWithTopic adds topic context to the search query.
func enhanceQueryWithTopic(query, topic string) string {
	topicContext := map[string]string{
		"murabaha":   "Murabaha cost-plus sale financing",
		"musharakah": "Musharakah partnership equity financing",
		"ijarah":     "Ijarah Islamic leasing contract",
		"sukuk":      "Sukuk Islamic bonds investment certificates",
		"takaful":    "Takaful Islamic insurance cooperative",
	}

	if context, ok := topicContext[topic]; ok {
		return fmt.Sprintf("%s %s", query, context)
	}
	return query
}

// formatAAOIFIResults formats the search results for the LLM.
func formatAAOIFIResults(chunks []storage.RetrievedChunk, query string) string {
	if len(chunks) == 0 {
		return fmt.Sprintf("No AAOIFI standards found for query: %q. Try rephrasing the search query or specifying a standard number (e.g., 'SS 8' for Murabaha).", query)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant AAOIFI standards:\n\n", len(chunks)))

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("[%d] ", i+1))

		// Standard number and title
		if chunk.StandardNum != "" {
			sb.WriteString(fmt.Sprintf("AAOIFI Shariah Standard %s", chunk.StandardNum))
			if chunk.DocumentTitle != "" && !strings.Contains(strings.ToLower(chunk.DocumentTitle), "shariah standard") {
				sb.WriteString(fmt.Sprintf(": %s", chunk.DocumentTitle))
			}
		} else {
			sb.WriteString(fmt.Sprintf("AAOIFI: %s", chunk.DocumentTitle))
		}
		sb.WriteString("\n")

		// Section information
		if chunk.SectionTitle != "" {
			sb.WriteString(fmt.Sprintf("Section: %s\n", chunk.SectionTitle))
		}

		// Page information
		if chunk.PageNumber > 0 {
			sb.WriteString(fmt.Sprintf("Page: %d\n", chunk.PageNumber))
		}

		// Relevance score
		sb.WriteString(fmt.Sprintf("Relevance: %.2f%%\n", chunk.Similarity*100))

		// Content excerpt
		sb.WriteString(fmt.Sprintf("Content:\n\"%s\"\n", truncateContent(chunk.Content, 500)))

		sb.WriteString("\n")
	}

	return sb.String()
}
