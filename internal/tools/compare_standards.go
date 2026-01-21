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

// CompareStandardsInput represents the input parameters for the comparison tool.
type CompareStandardsInput struct {
	Topic   string   `json:"topic"`
	Sources []string `json:"sources,omitempty"`
	Aspects []string `json:"aspects,omitempty"`
	TopK    int      `json:"top_k,omitempty"`
}

// SourceResult holds the retrieval result for a specific source.
type SourceResult struct {
	Source string
	Label  string
	Result *rag.RetrievalResult
	Error  error
}

// CompareStandardsTool implements the compare_standards tool.
type CompareStandardsTool struct {
	retriever *rag.Retriever
}

// NewCompareStandardsTool creates a new standards comparison tool.
func NewCompareStandardsTool(retriever *rag.Retriever) *CompareStandardsTool {
	return &CompareStandardsTool{
		retriever: retriever,
	}
}

// Name returns the tool name.
func (t *CompareStandardsTool) Name() string {
	return "compare_standards"
}

// Description returns the tool description.
func (t *CompareStandardsTool) Description() string {
	return `Compare Islamic finance standards and regulations across multiple authoritative sources. Use this tool when the user wants to understand differences or similarities between various Islamic banking standards and regulatory frameworks.

Available sources to compare:
- bnm: Bank Negara Malaysia regulations (Malaysian central bank)
- aaoifi: AAOIFI Shariah Standards (international)
- sc: Securities Commission Malaysia (capital markets)
- iifa: International Islamic Fiqh Academy resolutions (Majma Fiqh)
- fatwa: Malaysian State Fatwa Authority rulings

Default comparison: BNM vs AAOIFI (if no sources specified)

Common comparison topics:
- Murabaha ownership requirements
- Ijarah lease-to-own structures
- Musharakah profit/loss sharing
- Sukuk structuring requirements
- Takaful/Islamic insurance models
- Disclosure requirements
- Risk management standards
- Shariah compliance requirements`
}

// InputSchema returns the JSON schema for the tool input.
func (t *CompareStandardsTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"topic": map[string]any{
				"type":        "string",
				"description": "The Islamic finance topic to compare (e.g., 'murabaha ownership', 'ijarah requirements', 'profit recognition')",
			},
			"sources": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"bnm", "aaoifi", "sc", "iifa", "fatwa"},
				},
				"description": "Sources to compare. Choose 2 or more from: bnm, aaoifi, sc, iifa, fatwa. Default is ['bnm', 'aaoifi'].",
			},
			"aspects": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Specific aspects to compare (e.g., ['definition', 'requirements', 'disclosure']). If not provided, all relevant aspects will be compared.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"default":     3,
				"description": "Maximum number of results per source (1-5)",
			},
		},
		"required": []string{"topic"},
	}
}

// availableSources defines all available sources for comparison.
var availableSources = map[string]struct {
	Label    string
	FullName string
}{
	"bnm":    {Label: "BNM", FullName: "Bank Negara Malaysia"},
	"aaoifi": {Label: "AAOIFI", FullName: "Accounting and Auditing Organization for Islamic Financial Institutions"},
	"sc":     {Label: "SC", FullName: "Securities Commission Malaysia"},
	"iifa":   {Label: "IIFA", FullName: "International Islamic Fiqh Academy (Majma Fiqh)"},
	"fatwa":  {Label: "Fatwa", FullName: "Malaysian State Fatwa Authorities"},
}

// Execute runs the standards comparison tool.
func (t *CompareStandardsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params CompareStandardsInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("failed to parse input: %w", err)
	}

	if params.Topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	// Set defaults
	topK := params.TopK
	if topK <= 0 {
		topK = 3
	}
	if topK > 5 {
		topK = 5
	}

	// Determine which sources to compare
	sources := params.Sources
	if len(sources) == 0 {
		// Default to BNM and AAOIFI for backward compatibility
		sources = []string{"bnm", "aaoifi"}
	}

	// Validate sources
	for _, source := range sources {
		if _, ok := availableSources[source]; !ok {
			return "", fmt.Errorf("invalid source: %s. Available: bnm, aaoifi, sc, iifa, fatwa", source)
		}
	}

	if len(sources) < 2 {
		return "", fmt.Errorf("at least 2 sources required for comparison")
	}

	// Build enhanced query with aspects
	query := params.Topic
	if len(params.Aspects) > 0 {
		query = fmt.Sprintf("%s %s", params.Topic, strings.Join(params.Aspects, " "))
	}

	// Search all selected sources
	var results []SourceResult
	var successCount int

	for _, source := range sources {
		sourceInfo := availableSources[source]

		// Determine the source type for retrieval
		sourceType := source
		if source == "fatwa" {
			// For fatwa, we search across all states
			sourceType = "fatwa_federal" // Start with federal, we'll aggregate
		}

		opts := rag.RetrievalOptions{
			TopK:       topK,
			SourceType: sourceType,
		}

		result, err := t.retriever.Retrieve(ctx, query, opts)

		// For fatwa source, aggregate results from multiple states
		if source == "fatwa" && err == nil {
			result = t.aggregateFatwaResults(ctx, query, topK)
		}

		results = append(results, SourceResult{
			Source: source,
			Label:  sourceInfo.Label,
			Result: result,
			Error:  err,
		})

		if err == nil && result != nil && len(result.Chunks) > 0 {
			successCount++
		}
	}

	// Handle case where all searches failed
	if successCount == 0 {
		var errMsgs []string
		for _, r := range results {
			if r.Error != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("%s: %v", r.Label, r.Error))
			}
		}
		return "", fmt.Errorf("all searches failed: %s", strings.Join(errMsgs, "; "))
	}

	// Format comparison
	return formatMultiSourceComparison(params.Topic, params.Aspects, results), nil
}

// aggregateFatwaResults searches across all Malaysian state fatwa sources.
func (t *CompareStandardsTool) aggregateFatwaResults(ctx context.Context, query string, topK int) *rag.RetrievalResult {
	states := []string{
		"selangor", "johor", "penang", "federal", "perak", "kedah",
		"kelantan", "terengganu", "pahang", "nsembilan", "melaka",
		"perlis", "sabah", "sarawak",
	}

	var allChunks []storage.RetrievedChunk

	for _, state := range states {
		sourceType := fmt.Sprintf("fatwa_%s", state)
		opts := rag.RetrievalOptions{
			TopK:       topK,
			SourceType: sourceType,
		}

		result, err := t.retriever.Retrieve(ctx, query, opts)
		if err != nil {
			continue
		}

		allChunks = append(allChunks, result.Chunks...)
	}

	// Sort by similarity and take top K
	allChunks = sortAndLimitChunks(allChunks, topK)

	return &rag.RetrievalResult{
		Chunks: allChunks,
		Query:  query,
	}
}

// formatMultiSourceComparison formats comparison results from multiple sources.
func formatMultiSourceComparison(topic string, aspects []string, results []SourceResult) string {
	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("## Comparison: %s\n\n", toTitleCase(topic)))

	if len(aspects) > 0 {
		sb.WriteString(fmt.Sprintf("Aspects compared: %s\n\n", strings.Join(aspects, ", ")))
	}

	// List sources being compared
	var sourceLabels []string
	for _, r := range results {
		sourceLabels = append(sourceLabels, r.Label)
	}
	sb.WriteString(fmt.Sprintf("Sources: %s\n\n", strings.Join(sourceLabels, " vs ")))

	// Each source section
	for _, r := range results {
		sourceInfo := availableSources[r.Source]
		sb.WriteString(fmt.Sprintf("### %s (%s)\n\n", r.Label, sourceInfo.FullName))

		if r.Error != nil {
			sb.WriteString(fmt.Sprintf("*Error retrieving %s data: %v*\n\n", r.Label, r.Error))
		} else if r.Result == nil || len(r.Result.Chunks) == 0 {
			sb.WriteString(fmt.Sprintf("*No specific %s content found for this topic.*\n\n", r.Label))
		} else {
			for i, chunk := range r.Result.Chunks {
				sb.WriteString(formatComparisonChunk(i+1, chunk, r.Label))
			}
		}
	}

	// Analysis notes based on sources being compared
	sb.WriteString("### Comparison Notes\n\n")
	sb.WriteString("When comparing these sources, consider:\n\n")

	// Add source-specific notes
	for _, r := range results {
		switch r.Source {
		case "bnm":
			sb.WriteString("- **BNM**: Legally binding regulations for Malaysian financial institutions.\n")
		case "aaoifi":
			sb.WriteString("- **AAOIFI**: International standards adopted voluntarily by many jurisdictions worldwide.\n")
		case "sc":
			sb.WriteString("- **SC**: Securities Commission Malaysia regulations for Islamic capital markets and sukuk.\n")
		case "iifa":
			sb.WriteString("- **IIFA**: Scholarly consensus (ijma') from the International Islamic Fiqh Academy, widely referenced globally.\n")
		case "fatwa":
			sb.WriteString("- **Fatwa**: Malaysian state fatwa rulings, binding within their respective states.\n")
		}
	}

	sb.WriteString("\n**General considerations:**\n")
	sb.WriteString("1. Check effective dates as regulations may have been updated.\n")
	sb.WriteString("2. Local regulations may adapt international standards to market conditions.\n")
	sb.WriteString("3. Different sources may have different scopes and binding authority.\n")

	return sb.String()
}

// toTitleCase converts a string to title case.
func toTitleCase(s string) string {
	words := strings.Fields(s)
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(string(word[0])) + strings.ToLower(word[1:])
		}
	}
	return strings.Join(words, " ")
}

// formatComparisonChunk formats a single chunk for the comparison.
func formatComparisonChunk(index int, chunk storage.RetrievedChunk, source string) string {
	var sb strings.Builder

	// Source reference
	sb.WriteString(fmt.Sprintf("**[%s-%d]** ", source, index))

	// Document reference
	if chunk.StandardNum != "" {
		sb.WriteString(fmt.Sprintf("Standard %s", chunk.StandardNum))
		if chunk.DocumentTitle != "" {
			sb.WriteString(fmt.Sprintf(" - %s", chunk.DocumentTitle))
		}
	} else if chunk.DocumentTitle != "" {
		sb.WriteString(chunk.DocumentTitle)
	}
	sb.WriteString("\n")

	// Section and page
	if chunk.SectionTitle != "" {
		sb.WriteString(fmt.Sprintf("*Section: %s*", chunk.SectionTitle))
		if chunk.PageNumber > 0 {
			sb.WriteString(fmt.Sprintf(" | *Page %d*", chunk.PageNumber))
		}
		sb.WriteString("\n")
	} else if chunk.PageNumber > 0 {
		sb.WriteString(fmt.Sprintf("*Page %d*\n", chunk.PageNumber))
	}

	// Content
	content := truncateContent(chunk.Content, 400)
	sb.WriteString(fmt.Sprintf("\n> %s\n\n", content))

	return sb.String()
}
