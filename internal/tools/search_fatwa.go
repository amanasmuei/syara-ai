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

// SearchFatwaInput represents the input parameters for the state fatwa search tool.
type SearchFatwaInput struct {
	Query    string   `json:"query"`
	States   []string `json:"states,omitempty"`
	Category string   `json:"category,omitempty"`
	TopK     int      `json:"top_k,omitempty"`
}

// SearchFatwaTool implements the search_state_fatwa tool.
type SearchFatwaTool struct {
	retriever *rag.Retriever
}

// NewSearchFatwaTool creates a new state fatwa search tool.
func NewSearchFatwaTool(retriever *rag.Retriever) *SearchFatwaTool {
	return &SearchFatwaTool{
		retriever: retriever,
	}
}

// Name returns the tool name.
func (t *SearchFatwaTool) Name() string {
	return "search_state_fatwa"
}

// Description returns the tool description.
func (t *SearchFatwaTool) Description() string {
	return `Search Malaysian State Fatwa Authority rulings. Use this tool when looking for local Islamic rulings specific to Malaysian states on financial and religious matters.

States available:
- selangor: Selangor State Fatwa Committee
- johor: Johor State Fatwa Committee
- penang: Penang State Fatwa Committee
- federal: Federal Territory Fatwa Committee
- perak: Perak State Fatwa Committee
- kedah: Kedah State Fatwa Committee
- kelantan: Kelantan State Fatwa Committee
- terengganu: Terengganu State Fatwa Committee
- pahang: Pahang State Fatwa Committee
- nsembilan: Negeri Sembilan State Fatwa Committee
- melaka: Melaka State Fatwa Committee
- perlis: Perlis State Fatwa Committee
- sabah: Sabah State Fatwa Committee
- sarawak: Sarawak State Fatwa Committee
- all: Search all states (default)

Categories:
- muamalat: Financial and commercial transactions (Islamic banking, investments, business)
- ibadah: Worship and religious practices
- aqidah: Faith and beliefs
- social: Social and family issues (marriage, inheritance, etc.)
- general: All categories`
}

// InputSchema returns the JSON schema for the tool input.
func (t *SearchFatwaTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language search query for state fatwa rulings",
			},
			"states": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{
						"selangor", "johor", "penang", "federal", "perak", "kedah",
						"kelantan", "terengganu", "pahang", "nsembilan", "melaka",
						"perlis", "sabah", "sarawak", "all",
					},
				},
				"description": "States to search. Default is all states.",
			},
			"category": map[string]any{
				"type":        "string",
				"enum":        []string{"muamalat", "ibadah", "aqidah", "social", "general"},
				"description": "Category of fatwa to search. Default is 'general'.",
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

// Execute runs the state fatwa search tool.
func (t *SearchFatwaTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params SearchFatwaInput
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

	// Determine which states to search
	states := params.States
	if len(states) == 0 || containsAll(states) {
		// Search all fatwa sources
		states = getAllFatwaStates()
	}

	// Perform searches across selected states
	var allChunks []storage.RetrievedChunk

	for _, state := range states {
		sourceType := fmt.Sprintf("fatwa_%s", state)

		opts := rag.RetrievalOptions{
			TopK:       topK,
			SourceType: sourceType,
		}

		// Apply category filter
		if params.Category != "" && params.Category != "general" {
			opts.Categories = []string{params.Category}
		}

		// Perform retrieval for this state
		result, err := t.retriever.Retrieve(ctx, params.Query, opts)
		if err != nil {
			// Log error but continue with other states
			continue
		}

		allChunks = append(allChunks, result.Chunks...)
	}

	// Sort by similarity and take top K overall
	allChunks = sortAndLimitChunks(allChunks, topK)

	// Format results
	return formatFatwaResults(allChunks, params.Query, states), nil
}

// containsAll checks if the states slice contains "all".
func containsAll(states []string) bool {
	for _, s := range states {
		if strings.ToLower(s) == "all" {
			return true
		}
	}
	return false
}

// getAllFatwaStates returns all available fatwa state identifiers.
func getAllFatwaStates() []string {
	return []string{
		"selangor", "johor", "penang", "federal", "perak", "kedah",
		"kelantan", "terengganu", "pahang", "nsembilan", "melaka",
		"perlis", "sabah", "sarawak",
	}
}

// sortAndLimitChunks sorts chunks by similarity and returns top K.
func sortAndLimitChunks(chunks []storage.RetrievedChunk, topK int) []storage.RetrievedChunk {
	if len(chunks) <= topK {
		return chunks
	}

	// Simple bubble sort for small datasets (good enough for topK items)
	for i := 0; i < len(chunks)-1; i++ {
		for j := 0; j < len(chunks)-i-1; j++ {
			if chunks[j].Similarity < chunks[j+1].Similarity {
				chunks[j], chunks[j+1] = chunks[j+1], chunks[j]
			}
		}
	}

	return chunks[:topK]
}

// formatFatwaResults formats the search results for the LLM.
func formatFatwaResults(chunks []storage.RetrievedChunk, query string, states []string) string {
	if len(chunks) == 0 {
		stateList := strings.Join(states, ", ")
		return fmt.Sprintf("No state fatwa rulings found for query: %q in states: %s. Try broadening the search criteria or checking different states.", query, stateList)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant state fatwa rulings:\n\n", len(chunks)))

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("[%d] ", i+1))

		// State and title
		stateName := getStateName(chunk.SourceType)
		sb.WriteString(fmt.Sprintf("%s Fatwa", stateName))
		if chunk.StandardNum != "" {
			sb.WriteString(fmt.Sprintf(" No. %s", chunk.StandardNum))
		}
		sb.WriteString("\n")

		// Document title
		if chunk.DocumentTitle != "" {
			sb.WriteString(fmt.Sprintf("Title: %s\n", chunk.DocumentTitle))
		}

		// Category
		if chunk.Category != "" {
			sb.WriteString(fmt.Sprintf("Category: %s\n", formatFatwaCategory(chunk.Category)))
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
			sb.WriteString(fmt.Sprintf("Issued: %s\n", chunk.EffectiveDate.Format("2006-01-02")))
		}

		// Relevance score
		sb.WriteString(fmt.Sprintf("Relevance: %.2f%%\n", chunk.Similarity*100))

		// Content excerpt
		sb.WriteString(fmt.Sprintf("Content:\n\"%s\"\n", truncateContent(chunk.Content, 500)))

		sb.WriteString("\n")
	}

	sb.WriteString("\nNote: State fatwa rulings are binding within their respective Malaysian states. For federal-level guidance, refer to the Federal Territory fatwas.\n")

	return sb.String()
}

// getStateName converts source type to human-readable state name.
func getStateName(sourceType string) string {
	stateNames := map[string]string{
		"fatwa_selangor":   "Selangor",
		"fatwa_johor":      "Johor",
		"fatwa_penang":     "Penang",
		"fatwa_federal":    "Federal Territory",
		"fatwa_perak":      "Perak",
		"fatwa_kedah":      "Kedah",
		"fatwa_kelantan":   "Kelantan",
		"fatwa_terengganu": "Terengganu",
		"fatwa_pahang":     "Pahang",
		"fatwa_nsembilan":  "Negeri Sembilan",
		"fatwa_melaka":     "Melaka",
		"fatwa_perlis":     "Perlis",
		"fatwa_sabah":      "Sabah",
		"fatwa_sarawak":    "Sarawak",
	}

	if name, ok := stateNames[sourceType]; ok {
		return name
	}

	// Extract state from source type
	if strings.HasPrefix(sourceType, "fatwa_") {
		state := strings.TrimPrefix(sourceType, "fatwa_")
		return strings.Title(state)
	}

	return sourceType
}

// formatFatwaCategory converts internal category to display format.
func formatFatwaCategory(category string) string {
	categories := map[string]string{
		"muamalat": "Muamalat (Financial/Commercial)",
		"ibadah":   "Ibadah (Worship)",
		"aqidah":   "Aqidah (Faith/Belief)",
		"social":   "Social/Family",
		"general":  "General",
	}

	if display, ok := categories[category]; ok {
		return display
	}

	return strings.Title(category)
}
