// Package tools provides agent tools for the ShariaComply AI system.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/alqutdigital/islamic-banking-agent/internal/rag"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
)

// SearchIIFAInput represents the input parameters for the IIFA search tool.
type SearchIIFAInput struct {
	Query            string `json:"query"`
	ResolutionNumber string `json:"resolution_number,omitempty"`
	Topic            string `json:"topic,omitempty"`
	TopK             int    `json:"top_k,omitempty"`
}

// SearchIIFATool implements the search_iifa_resolutions tool.
type SearchIIFATool struct {
	retriever *rag.Retriever
}

// NewSearchIIFATool creates a new IIFA (Majma Fiqh) search tool.
func NewSearchIIFATool(retriever *rag.Retriever) *SearchIIFATool {
	return &SearchIIFATool{
		retriever: retriever,
	}
}

// Name returns the tool name.
func (t *SearchIIFATool) Name() string {
	return "search_iifa_resolutions"
}

// Description returns the tool description.
func (t *SearchIIFATool) Description() string {
	return `Search International Islamic Fiqh Academy (IIFA/Majma Fiqh) resolutions. The IIFA is an international body that issues authoritative fiqh rulings on contemporary Islamic issues. Use this tool for international fiqh rulings on Islamic finance topics, including banking, insurance, and commercial transactions.

Key resolution topics:
- Islamic banking and finance contracts (Murabaha, Ijarah, Musharakah)
- Insurance (Takaful) rulings
- Contemporary commercial issues
- Currency and money matters
- Sukuk and securities
- Investment and wealth management

Specify resolution_number to find a specific resolution (e.g., "267" for Resolution No. 267).

Topics available:
- banking: Islamic banking contracts and transactions
- insurance: Takaful and cooperative insurance
- commerce: Commercial transactions and trade
- currency: Currency exchange and money matters
- investment: Investment and wealth management
- general: General fiqh matters`
}

// InputSchema returns the JSON schema for the tool input.
func (t *SearchIIFATool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language search query for IIFA resolutions",
			},
			"resolution_number": map[string]any{
				"type":        "string",
				"description": "Specific resolution number to search for (e.g., '267' for Resolution No. 267)",
			},
			"topic": map[string]any{
				"type":        "string",
				"enum":        []string{"banking", "insurance", "commerce", "currency", "investment", "general"},
				"description": "Topic category to filter resolutions",
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

// Execute runs the IIFA search tool.
func (t *SearchIIFATool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params SearchIIFAInput
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
		SourceType: "iifa",
	}

	// Normalize and apply resolution number filter
	if params.ResolutionNumber != "" {
		normalizedNum := normalizeResolutionNumber(params.ResolutionNumber)
		opts.StandardNumber = normalizedNum
	}

	// Apply topic filter as category
	if params.Topic != "" && params.Topic != "general" {
		opts.Categories = []string{params.Topic}
	}

	// Enhance query with topic context if specified
	query := params.Query
	if params.Topic != "" {
		query = enhanceQueryWithIIFATopic(query, params.Topic)
	}

	// Perform retrieval
	result, err := t.retriever.Retrieve(ctx, query, opts)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	// Format results
	return formatIIFAResults(result.Chunks, params.Query), nil
}

// normalizeResolutionNumber normalizes various resolution number formats.
func normalizeResolutionNumber(input string) string {
	// Remove common prefixes
	input = strings.TrimSpace(input)
	re := regexp.MustCompile(`(?i)^(?:Resolution\s+(?:No\.?\s*)?|Res\.?\s*)`)
	input = re.ReplaceAllString(input, "")

	// Extract just the number
	reNum := regexp.MustCompile(`(\d+)`)
	matches := reNum.FindStringSubmatch(input)
	if len(matches) > 1 {
		return matches[1]
	}

	return input
}

// enhanceQueryWithIIFATopic adds topic-specific context to the query.
func enhanceQueryWithIIFATopic(query, topic string) string {
	topicContext := map[string]string{
		"banking":    "Islamic banking financial contracts murabaha ijarah musharakah mudharabah",
		"insurance":  "takaful Islamic insurance cooperative mutual insurance",
		"commerce":   "commercial transactions trade business sale contract",
		"currency":   "currency exchange money sarf foreign exchange",
		"investment": "investment portfolio wealth management sukuk bonds",
	}

	if context, ok := topicContext[topic]; ok {
		return fmt.Sprintf("%s %s", query, context)
	}
	return query
}

// formatIIFAResults formats the search results for the LLM.
func formatIIFAResults(chunks []storage.RetrievedChunk, query string) string {
	if len(chunks) == 0 {
		return fmt.Sprintf("No IIFA (Majma Fiqh) resolutions found for query: %q. Try rephrasing the search query or removing filters.", query)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant IIFA resolutions:\n\n", len(chunks)))

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("[%d] ", i+1))

		// Resolution title
		sb.WriteString("IIFA Resolution")
		if chunk.StandardNum != "" {
			sb.WriteString(fmt.Sprintf(" No. %s", chunk.StandardNum))
		}
		sb.WriteString("\n")

		// Document title
		if chunk.DocumentTitle != "" {
			sb.WriteString(fmt.Sprintf("Title: %s\n", chunk.DocumentTitle))
		}

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

	sb.WriteString("\nNote: IIFA resolutions represent scholarly consensus (ijma') and are widely referenced in Islamic finance globally.\n")

	return sb.String()
}
