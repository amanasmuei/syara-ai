// Package rag provides context building for LLM prompts.
package rag

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
)

// ContextBuilder builds LLM context from retrieved chunks.
type ContextBuilder struct {
	maxTokens  int
	logger     *slog.Logger
	config     ContextBuilderConfig
}

// ContextBuilderConfig holds configuration for the context builder.
type ContextBuilderConfig struct {
	MaxTokens              int
	ChunkSeparator         string
	IncludeSourceMetadata  bool
	DeduplicateContent     bool
	DeduplicationThreshold float64 // Similarity threshold for deduplication (0-1)
	FormatStyle            FormatStyle
}

// FormatStyle defines how chunks are formatted in the context.
type FormatStyle string

const (
	FormatStyleDetailed FormatStyle = "detailed" // Full metadata
	FormatStyleCompact  FormatStyle = "compact"  // Minimal metadata
	FormatStyleRaw      FormatStyle = "raw"      // Content only
)

// DefaultContextBuilderConfig returns a default configuration.
func DefaultContextBuilderConfig() ContextBuilderConfig {
	return ContextBuilderConfig{
		MaxTokens:              3000,
		ChunkSeparator:         "\n\n---\n\n",
		IncludeSourceMetadata:  true,
		DeduplicateContent:     true,
		DeduplicationThreshold: 0.85,
		FormatStyle:            FormatStyleDetailed,
	}
}

// ContextOptions represents options for building context.
type ContextOptions struct {
	MaxTokens             int
	IncludeSourceMetadata bool
	FormatStyle           FormatStyle
	SystemPromptTokens    int // Reserve tokens for system prompt
	QueryTokens           int // Reserve tokens for user query
	ResponseBuffer        int // Reserve tokens for response
}

// BuiltContext represents the result of context building.
type BuiltContext struct {
	Text           string                     `json:"text"`
	TokenCount     int                        `json:"token_count"`
	IncludedChunks []storage.RetrievedChunk   `json:"included_chunks"`
	TruncatedCount int                        `json:"truncated_count"`
	Citations      []Citation                 `json:"citations"`
}

// Citation represents a source citation.
type Citation struct {
	Index         int     `json:"index"`
	ChunkID       string  `json:"chunk_id"`
	DocumentID    string  `json:"document_id"`
	DocumentTitle string  `json:"document_title"`
	SourceType    string  `json:"source_type"`
	Section       string  `json:"section,omitempty"`
	Page          int     `json:"page,omitempty"`
	StandardNum   string  `json:"standard_number,omitempty"`
	Similarity    float64 `json:"similarity"`
}

// NewContextBuilder creates a new ContextBuilder instance.
func NewContextBuilder(logger *slog.Logger, config ContextBuilderConfig) *ContextBuilder {
	if logger == nil {
		logger = slog.Default()
	}

	// Apply defaults
	if config.MaxTokens == 0 {
		config.MaxTokens = 3000
	}
	if config.ChunkSeparator == "" {
		config.ChunkSeparator = "\n\n---\n\n"
	}
	if config.DeduplicationThreshold == 0 {
		config.DeduplicationThreshold = 0.85
	}

	return &ContextBuilder{
		maxTokens: config.MaxTokens,
		logger:    logger.With("component", "context_builder"),
		config:    config,
	}
}

// Build builds context from retrieved chunks.
func (cb *ContextBuilder) Build(chunks []storage.RetrievedChunk, opts ContextOptions) (*BuiltContext, error) {
	if len(chunks) == 0 {
		return &BuiltContext{}, nil
	}

	// Apply defaults
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = cb.maxTokens
	}

	// Adjust for reserved tokens
	availableTokens := maxTokens - opts.SystemPromptTokens - opts.QueryTokens - opts.ResponseBuffer
	if availableTokens <= 0 {
		availableTokens = maxTokens
	}

	cb.logger.Debug("building context",
		"chunks", len(chunks),
		"max_tokens", maxTokens,
		"available_tokens", availableTokens,
	)

	// Sort chunks by similarity (should already be sorted, but ensure)
	sortedChunks := make([]storage.RetrievedChunk, len(chunks))
	copy(sortedChunks, chunks)
	sort.Slice(sortedChunks, func(i, j int) bool {
		return sortedChunks[i].Similarity > sortedChunks[j].Similarity
	})

	// Deduplicate if enabled
	if cb.config.DeduplicateContent {
		sortedChunks = cb.deduplicateChunks(sortedChunks)
	}

	// Build context within token budget
	var contextBuilder strings.Builder
	var includedChunks []storage.RetrievedChunk
	var citations []Citation
	tokenCount := 0
	truncatedCount := 0

	for i, chunk := range sortedChunks {
		// Format the chunk
		formatted := cb.formatChunk(chunk, i+1, opts)
		chunkTokens := cb.estimateTokens(formatted)

		// Check if adding this chunk exceeds the budget
		if tokenCount+chunkTokens > availableTokens {
			truncatedCount = len(sortedChunks) - len(includedChunks)
			cb.logger.Debug("token budget reached",
				"included", len(includedChunks),
				"truncated", truncatedCount,
				"token_count", tokenCount,
			)
			break
		}

		// Add separator if not first chunk
		if contextBuilder.Len() > 0 {
			contextBuilder.WriteString(cb.config.ChunkSeparator)
			tokenCount += cb.estimateTokens(cb.config.ChunkSeparator)
		}

		contextBuilder.WriteString(formatted)
		tokenCount += chunkTokens
		includedChunks = append(includedChunks, chunk)

		// Build citation
		citation := Citation{
			Index:         i + 1,
			ChunkID:       chunk.ID.String(),
			DocumentID:    chunk.DocumentID.String(),
			DocumentTitle: chunk.DocumentTitle,
			SourceType:    chunk.SourceType,
			Section:       chunk.SectionTitle,
			Page:          chunk.PageNumber,
			StandardNum:   chunk.StandardNum,
			Similarity:    chunk.Similarity,
		}
		citations = append(citations, citation)
	}

	result := &BuiltContext{
		Text:           contextBuilder.String(),
		TokenCount:     tokenCount,
		IncludedChunks: includedChunks,
		TruncatedCount: truncatedCount,
		Citations:      citations,
	}

	cb.logger.Info("context built",
		"included_chunks", len(includedChunks),
		"truncated_chunks", truncatedCount,
		"token_count", tokenCount,
	)

	return result, nil
}

// formatChunk formats a chunk based on the style.
func (cb *ContextBuilder) formatChunk(chunk storage.RetrievedChunk, index int, opts ContextOptions) string {
	style := opts.FormatStyle
	if style == "" {
		style = cb.config.FormatStyle
	}

	switch style {
	case FormatStyleDetailed:
		return cb.formatDetailed(chunk, index)
	case FormatStyleCompact:
		return cb.formatCompact(chunk, index)
	case FormatStyleRaw:
		return chunk.Content
	default:
		return cb.formatDetailed(chunk, index)
	}
}

// formatDetailed formats a chunk with full metadata.
func (cb *ContextBuilder) formatDetailed(chunk storage.RetrievedChunk, index int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("[Source %d]\n", index))
	sb.WriteString(fmt.Sprintf("Document: %s\n", chunk.DocumentTitle))

	if chunk.SourceType != "" {
		sb.WriteString(fmt.Sprintf("Source Type: %s\n", chunk.SourceType))
	}

	if chunk.SectionTitle != "" {
		sb.WriteString(fmt.Sprintf("Section: %s\n", chunk.SectionTitle))
	}

	if chunk.PageNumber > 0 {
		sb.WriteString(fmt.Sprintf("Page: %d\n", chunk.PageNumber))
	}

	if chunk.StandardNum != "" {
		sb.WriteString(fmt.Sprintf("Standard: %s\n", chunk.StandardNum))
	}

	if chunk.Category != "" {
		sb.WriteString(fmt.Sprintf("Category: %s\n", chunk.Category))
	}

	sb.WriteString(fmt.Sprintf("Relevance: %.2f\n", chunk.Similarity))
	sb.WriteString("\n")
	sb.WriteString(chunk.Content)

	return sb.String()
}

// formatCompact formats a chunk with minimal metadata.
func (cb *ContextBuilder) formatCompact(chunk storage.RetrievedChunk, index int) string {
	var sb strings.Builder

	// Compact header
	header := fmt.Sprintf("[%d] %s", index, chunk.DocumentTitle)
	if chunk.PageNumber > 0 {
		header += fmt.Sprintf(" (p.%d)", chunk.PageNumber)
	}
	if chunk.StandardNum != "" {
		header += fmt.Sprintf(" [%s]", chunk.StandardNum)
	}

	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(chunk.Content)

	return sb.String()
}

// deduplicateChunks removes chunks with similar content.
func (cb *ContextBuilder) deduplicateChunks(chunks []storage.RetrievedChunk) []storage.RetrievedChunk {
	if len(chunks) <= 1 {
		return chunks
	}

	deduplicated := make([]storage.RetrievedChunk, 0, len(chunks))
	seen := make(map[string]bool)

	for _, chunk := range chunks {
		// Create a normalized key for comparison
		normalizedContent := cb.normalizeContent(chunk.Content)

		// Check for exact or near duplicates
		isDuplicate := false
		for seenContent := range seen {
			if cb.isSimilarContent(normalizedContent, seenContent) {
				isDuplicate = true
				cb.logger.Debug("deduplicating chunk",
					"chunk_id", chunk.ID,
					"similarity_threshold", cb.config.DeduplicationThreshold,
				)
				break
			}
		}

		if !isDuplicate {
			deduplicated = append(deduplicated, chunk)
			seen[normalizedContent] = true
		}
	}

	if len(deduplicated) < len(chunks) {
		cb.logger.Info("deduplicated chunks",
			"original", len(chunks),
			"deduplicated", len(deduplicated),
			"removed", len(chunks)-len(deduplicated),
		)
	}

	return deduplicated
}

// normalizeContent normalizes content for comparison.
func (cb *ContextBuilder) normalizeContent(content string) string {
	// Convert to lowercase and remove extra whitespace
	normalized := strings.ToLower(content)
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized
}

// isSimilarContent checks if two pieces of content are similar.
func (cb *ContextBuilder) isSimilarContent(a, b string) bool {
	// Use Jaccard similarity for simple comparison
	similarity := cb.jaccardSimilarity(a, b)
	return similarity >= cb.config.DeduplicationThreshold
}

// jaccardSimilarity computes Jaccard similarity between two strings.
func (cb *ContextBuilder) jaccardSimilarity(a, b string) float64 {
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)

	setA := make(map[string]bool)
	for _, w := range wordsA {
		setA[w] = true
	}

	setB := make(map[string]bool)
	for _, w := range wordsB {
		setB[w] = true
	}

	// Count intersection
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}

	// Count union
	union := len(setA) + len(setB) - intersection

	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

// estimateTokens estimates the number of tokens in a string.
// This is a simple estimation: ~4 characters per token for English text.
func (cb *ContextBuilder) estimateTokens(text string) int {
	// Simple heuristic: approximately 4 characters per token
	// This is a rough estimate; for production, use tiktoken or similar
	return (len(text) + 3) / 4
}

// BuildPrompt builds a complete prompt with context and query.
func (cb *ContextBuilder) BuildPrompt(context *BuiltContext, query string, systemPrompt string) string {
	var sb strings.Builder

	if systemPrompt != "" {
		sb.WriteString(systemPrompt)
		sb.WriteString("\n\n")
	}

	if context != nil && context.Text != "" {
		sb.WriteString("## Relevant Context\n\n")
		sb.WriteString(context.Text)
		sb.WriteString("\n\n")
	}

	sb.WriteString("## User Question\n\n")
	sb.WriteString(query)

	return sb.String()
}

// BuildCitationText builds a formatted citation list.
func (cb *ContextBuilder) BuildCitationText(citations []Citation) string {
	if len(citations) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Sources\n\n")

	for _, c := range citations {
		sb.WriteString(fmt.Sprintf("[%d] %s", c.Index, c.DocumentTitle))
		if c.Page > 0 {
			sb.WriteString(fmt.Sprintf(", Page %d", c.Page))
		}
		if c.Section != "" {
			sb.WriteString(fmt.Sprintf(", Section: %s", c.Section))
		}
		if c.StandardNum != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", c.StandardNum))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// GetIncludedDocumentIDs returns unique document IDs from the context.
func (cb *ContextBuilder) GetIncludedDocumentIDs(context *BuiltContext) []string {
	if context == nil || len(context.IncludedChunks) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var ids []string

	for _, chunk := range context.IncludedChunks {
		id := chunk.DocumentID.String()
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	return ids
}
