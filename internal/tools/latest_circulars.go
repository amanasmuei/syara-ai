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

// GetLatestCircularsInput represents the input parameters for the latest circulars tool.
type GetLatestCircularsInput struct {
	Days  int    `json:"days,omitempty"`
	Topic string `json:"topic,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// GetLatestCircularsTool implements the get_latest_circulars tool.
type GetLatestCircularsTool struct {
	retriever *rag.Retriever
}

// NewGetLatestCircularsTool creates a new latest circulars tool.
func NewGetLatestCircularsTool(retriever *rag.Retriever) *GetLatestCircularsTool {
	return &GetLatestCircularsTool{
		retriever: retriever,
	}
}

// Name returns the tool name.
func (t *GetLatestCircularsTool) Name() string {
	return "get_latest_circulars"
}

// Description returns the tool description.
func (t *GetLatestCircularsTool) Description() string {
	return `Get the latest BNM circulars and regulatory updates. Use this tool when users ask about recent changes, new regulations, "what's new", or the latest updates in Islamic banking regulations.

This tool returns documents sorted by date, showing the most recent circulars and policy updates.`
}

// InputSchema returns the JSON schema for the tool input.
func (t *GetLatestCircularsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"days": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     365,
				"default":     30,
				"description": "Number of days to look back (default: 30, max: 365)",
			},
			"topic": map[string]interface{}{
				"type":        "string",
				"description": "Optional topic filter (e.g., 'murabaha', 'digital banking', 'disclosure')",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     10,
				"default":     5,
				"description": "Maximum number of results to return (default: 5, max: 10)",
			},
		},
	}
}

// Execute runs the latest circulars tool.
func (t *GetLatestCircularsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params GetLatestCircularsInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("failed to parse input: %w", err)
	}

	// Set defaults
	days := params.Days
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	// Calculate date range
	now := time.Now()
	fromDate := now.AddDate(0, 0, -days)

	// Build retrieval options
	opts := rag.RetrievalOptions{
		TopK:       limit,
		SourceType: "bnm",
		Categories: []string{"circulars", "policy_documents"},
		DateRange: storage.DateRange{
			Start: fromDate,
			End:   now,
		},
	}

	// Build query
	query := "latest regulatory updates circulars"
	if params.Topic != "" {
		query = fmt.Sprintf("latest %s regulations updates", params.Topic)
	}

	// Perform retrieval
	result, err := t.retriever.Retrieve(ctx, query, opts)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	// Format results
	return formatLatestCirculars(result.Chunks, days, params.Topic), nil
}

// formatLatestCirculars formats the circulars for display.
func formatLatestCirculars(chunks []storage.RetrievedChunk, days int, topic string) string {
	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("## Latest BNM Circulars (Last %d Days)\n\n", days))

	if topic != "" {
		sb.WriteString(fmt.Sprintf("Topic filter: %s\n\n", topic))
	}

	if len(chunks) == 0 {
		sb.WriteString("No new circulars or regulatory updates found for the specified period.\n\n")
		sb.WriteString("This could mean:\n")
		sb.WriteString("- No new regulations have been issued in this period\n")
		sb.WriteString("- The documents haven't been indexed yet\n")
		sb.WriteString("- Try expanding the date range using a larger 'days' value\n")
		return sb.String()
	}

	// Sort by effective date (most recent first) - chunks should already be sorted by relevance
	// but we present them in a list format

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("**%d. %s**\n", i+1, chunk.DocumentTitle))

		// Date information
		if !chunk.EffectiveDate.IsZero() {
			dateStr := chunk.EffectiveDate.Format("January 2, 2006")
			daysAgo := int(time.Since(chunk.EffectiveDate).Hours() / 24)
			if daysAgo == 0 {
				sb.WriteString(fmt.Sprintf("   Date: %s (Today)\n", dateStr))
			} else if daysAgo == 1 {
				sb.WriteString(fmt.Sprintf("   Date: %s (Yesterday)\n", dateStr))
			} else {
				sb.WriteString(fmt.Sprintf("   Date: %s (%d days ago)\n", dateStr, daysAgo))
			}
		}

		// Category
		if chunk.Category != "" {
			sb.WriteString(fmt.Sprintf("   Category: %s\n", formatCategory(chunk.Category)))
		}

		// Standard number if available
		if chunk.StandardNum != "" {
			sb.WriteString(fmt.Sprintf("   Reference: %s\n", chunk.StandardNum))
		}

		// Summary (first part of content)
		summary := extractSummary(chunk.Content, 200)
		sb.WriteString(fmt.Sprintf("   Summary: %s\n", summary))

		sb.WriteString("\n")
	}

	// Footer with tips
	sb.WriteString("---\n")
	sb.WriteString("*Use the search tools to get more detailed information about specific circulars.*\n")

	return sb.String()
}

// extractSummary extracts a brief summary from the content.
func extractSummary(content string, maxLen int) string {
	// Clean up whitespace
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\n", " ")
	for strings.Contains(content, "  ") {
		content = strings.ReplaceAll(content, "  ", " ")
	}

	if len(content) <= maxLen {
		return content
	}

	// Find sentence boundary
	truncated := content[:maxLen]

	// Try to end at a sentence
	lastPeriod := strings.LastIndex(truncated, ". ")
	if lastPeriod > maxLen-80 {
		return truncated[:lastPeriod+1]
	}

	// Otherwise, end at word boundary
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen-30 {
		return truncated[:lastSpace] + "..."
	}

	return truncated + "..."
}
