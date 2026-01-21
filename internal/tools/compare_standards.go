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
	Aspects []string `json:"aspects,omitempty"`
	TopK    int      `json:"top_k,omitempty"`
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
	return `Compare BNM (Bank Negara Malaysia) regulations with AAOIFI Shariah Standards on a specific topic. Use this tool when the user wants to understand differences or similarities between Malaysian and international Islamic banking standards.

This tool searches both BNM and AAOIFI sources and presents a side-by-side comparison.

Common comparison topics:
- Murabaha ownership requirements
- Ijarah lease-to-own structures
- Musharakah profit/loss sharing
- Sukuk structuring requirements
- Takaful/Islamic insurance models
- Disclosure requirements
- Risk management standards`
}

// InputSchema returns the JSON schema for the tool input.
func (t *CompareStandardsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"topic": map[string]interface{}{
				"type":        "string",
				"description": "The Islamic finance topic to compare (e.g., 'murabaha ownership', 'ijarah requirements', 'profit recognition')",
			},
			"aspects": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
				"description": "Specific aspects to compare (e.g., ['definition', 'requirements', 'disclosure']). If not provided, all relevant aspects will be compared.",
			},
			"top_k": map[string]interface{}{
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

	// Build enhanced query with aspects
	query := params.Topic
	if len(params.Aspects) > 0 {
		query = fmt.Sprintf("%s %s", params.Topic, strings.Join(params.Aspects, " "))
	}

	// Search BNM regulations
	bnmOpts := rag.RetrievalOptions{
		TopK:       topK,
		SourceType: "bnm",
	}
	bnmResult, bnmErr := t.retriever.Retrieve(ctx, query, bnmOpts)

	// Search AAOIFI standards
	aaoifiOpts := rag.RetrievalOptions{
		TopK:       topK,
		SourceType: "aaoifi",
	}
	aaoifiResult, aaoifiErr := t.retriever.Retrieve(ctx, query, aaoifiOpts)

	// Handle errors
	if bnmErr != nil && aaoifiErr != nil {
		return "", fmt.Errorf("both searches failed: BNM: %v, AAOIFI: %v", bnmErr, aaoifiErr)
	}

	// Format comparison
	return formatComparison(params.Topic, params.Aspects, bnmResult, aaoifiResult, bnmErr, aaoifiErr), nil
}

// formatComparison formats the comparison results for the LLM.
func formatComparison(topic string, aspects []string, bnmResult, aaoifiResult *rag.RetrievalResult, bnmErr, aaoifiErr error) string {
	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("## Comparison: %s\n\n", strings.Title(topic)))

	if len(aspects) > 0 {
		sb.WriteString(fmt.Sprintf("Aspects compared: %s\n\n", strings.Join(aspects, ", ")))
	}

	// BNM Section
	sb.WriteString("### BNM (Bank Negara Malaysia) Requirements\n\n")
	if bnmErr != nil {
		sb.WriteString(fmt.Sprintf("*Error retrieving BNM data: %v*\n\n", bnmErr))
	} else if bnmResult == nil || len(bnmResult.Chunks) == 0 {
		sb.WriteString("*No specific BNM regulations found for this topic.*\n\n")
	} else {
		for i, chunk := range bnmResult.Chunks {
			sb.WriteString(formatComparisonChunk(i+1, chunk, "BNM"))
		}
	}

	// AAOIFI Section
	sb.WriteString("### AAOIFI (International) Requirements\n\n")
	if aaoifiErr != nil {
		sb.WriteString(fmt.Sprintf("*Error retrieving AAOIFI data: %v*\n\n", aaoifiErr))
	} else if aaoifiResult == nil || len(aaoifiResult.Chunks) == 0 {
		sb.WriteString("*No specific AAOIFI standards found for this topic.*\n\n")
	} else {
		for i, chunk := range aaoifiResult.Chunks {
			sb.WriteString(formatComparisonChunk(i+1, chunk, "AAOIFI"))
		}
	}

	// Analysis notes
	sb.WriteString("### Comparison Notes\n\n")
	sb.WriteString("When comparing BNM and AAOIFI requirements, consider:\n")
	sb.WriteString("1. **Jurisdiction**: BNM requirements are specific to Malaysia, while AAOIFI represents international standards.\n")
	sb.WriteString("2. **Binding nature**: BNM regulations are legally binding for Malaysian financial institutions, while AAOIFI standards are adopted voluntarily by many jurisdictions.\n")
	sb.WriteString("3. **Flexibility**: BNM may have adapted international standards to local market conditions.\n")
	sb.WriteString("4. **Updates**: Check the effective dates as regulations may have been updated.\n")

	return sb.String()
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
