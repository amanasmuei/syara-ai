// Package chunker provides semantic text chunking for RAG applications.
package chunker

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/pkoukk/tiktoken-go"
)

// ChunkerConfig holds configuration for the semantic chunker.
type ChunkerConfig struct {
	MaxTokens      int  // Maximum tokens per chunk (default: 512)
	OverlapTokens  int  // Overlap tokens between chunks (default: 50)
	MinChunkSize   int  // Minimum tokens for a valid chunk (default: 100)
	PreserveParent bool // Keep parent section context in metadata
	Model          string // Model for tokenization (default: "cl100k_base" for text-embedding-3-small)
}

// DefaultChunkerConfig returns default chunker configuration.
func DefaultChunkerConfig() ChunkerConfig {
	return ChunkerConfig{
		MaxTokens:      512,
		OverlapTokens:  50,
		MinChunkSize:   100,
		PreserveParent: true,
		Model:          "cl100k_base",
	}
}

// ChunkMetadata holds metadata about a document being chunked.
type ChunkMetadata struct {
	DocumentID   string                 `json:"document_id"`
	SourceType   string                 `json:"source_type"`
	DocumentTitle string                `json:"document_title"`
	PageNumber   int                    `json:"page_number,omitempty"`
	Extra        map[string]interface{} `json:"extra,omitempty"`
}

// Chunk represents a text chunk with metadata.
type Chunk struct {
	ID               string                 `json:"id"`
	Content          string                 `json:"content"`
	TokenCount       int                    `json:"token_count"`
	PageNumber       int                    `json:"page_number,omitempty"`
	SectionTitle     string                 `json:"section_title,omitempty"`
	SectionHierarchy []string               `json:"section_hierarchy,omitempty"`
	ParentID         string                 `json:"parent_id,omitempty"`
	ChunkIndex       int                    `json:"chunk_index"`
	StartChar        int                    `json:"start_char"`
	EndChar          int                    `json:"end_char"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

// Section represents a document section.
type Section struct {
	Number   string     `json:"number,omitempty"`
	Level    int        `json:"level"`
	Title    string     `json:"title"`
	Content  string     `json:"content"`
	StartPos int        `json:"start_pos"`
	EndPos   int        `json:"end_pos"`
	Children []*Section `json:"children,omitempty"`
	Parent   *Section   `json:"-"`
}

// SemanticChunker splits text into semantic chunks.
type SemanticChunker struct {
	config    ChunkerConfig
	tokenizer *tiktoken.Tiktoken
}

// NewSemanticChunker creates a new semantic chunker.
func NewSemanticChunker(cfg ChunkerConfig) (*SemanticChunker, error) {
	tokenizer, err := tiktoken.GetEncoding(cfg.Model)
	if err != nil {
		// Fallback to cl100k_base
		tokenizer, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return nil, fmt.Errorf("failed to initialize tokenizer: %w", err)
		}
	}

	return &SemanticChunker{
		config:    cfg,
		tokenizer: tokenizer,
	}, nil
}

// Chunk splits text into semantic chunks with the given metadata.
func (c *SemanticChunker) Chunk(text string, metadata ChunkMetadata) []Chunk {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	// First, try to split by sections
	sections := c.splitBySection(text)

	var chunks []Chunk
	chunkIndex := 0

	for _, section := range sections {
		sectionChunks := c.chunkSection(section, metadata, &chunkIndex)
		chunks = append(chunks, sectionChunks...)
	}

	// Add overlap between chunks
	chunks = c.addOverlap(chunks)

	return chunks
}

// ChunkPages chunks text with page-level awareness.
func (c *SemanticChunker) ChunkPages(pages []PageContent, metadata ChunkMetadata) []Chunk {
	var allChunks []Chunk
	chunkIndex := 0

	for _, page := range pages {
		pageMetadata := metadata
		pageMetadata.PageNumber = page.PageNumber

		sections := c.splitBySection(page.Text)

		for _, section := range sections {
			sectionChunks := c.chunkSection(section, pageMetadata, &chunkIndex)
			for i := range sectionChunks {
				sectionChunks[i].PageNumber = page.PageNumber
			}
			allChunks = append(allChunks, sectionChunks...)
		}
	}

	// Add overlap between chunks
	allChunks = c.addOverlap(allChunks)

	return allChunks
}

// PageContent represents a page with its text content.
type PageContent struct {
	PageNumber int    `json:"page_number"`
	Text       string `json:"text"`
}

// splitBySection splits text into sections based on headers.
func (c *SemanticChunker) splitBySection(text string) []*Section {
	// Define patterns for section headers
	patterns := []struct {
		regex *regexp.Regexp
		level int
	}{
		// Markdown-style headers
		{regexp.MustCompile(`(?m)^#{1}\s+(.+)$`), 1},
		{regexp.MustCompile(`(?m)^#{2}\s+(.+)$`), 2},
		{regexp.MustCompile(`(?m)^#{3}\s+(.+)$`), 3},
		// Numbered sections (1. Title, 1.1 Subtitle, etc.)
		{regexp.MustCompile(`(?m)^(\d+)\.\s+(.+)$`), 1},
		{regexp.MustCompile(`(?m)^(\d+\.\d+)\s+(.+)$`), 2},
		{regexp.MustCompile(`(?m)^(\d+\.\d+\.\d+)\s+(.+)$`), 3},
		// ARTICLE/SECTION style (common in legal documents)
		{regexp.MustCompile(`(?mi)^(?:ARTICLE|SECTION|CHAPTER)\s+(\d+|[IVXLC]+)[:\s]+(.+)$`), 1},
		// All caps titles (often section headers)
		{regexp.MustCompile(`(?m)^([A-Z][A-Z\s]{10,})$`), 1},
	}

	// Find all section boundaries
	var boundaries []struct {
		pos    int
		level  int
		title  string
		number string
	}

	for _, p := range patterns {
		matches := p.regex.FindAllStringSubmatchIndex(text, -1)
		for _, match := range matches {
			if len(match) >= 4 {
				var title, number string
				if len(match) >= 6 {
					// Has number and title groups
					number = text[match[2]:match[3]]
					title = strings.TrimSpace(text[match[4]:match[5]])
				} else {
					title = strings.TrimSpace(text[match[2]:match[3]])
				}

				boundaries = append(boundaries, struct {
					pos    int
					level  int
					title  string
					number string
				}{
					pos:    match[0],
					level:  p.level,
					title:  title,
					number: number,
				})
			}
		}
	}

	// Sort boundaries by position
	sortBoundaries(boundaries)

	// Remove duplicates (same position)
	boundaries = deduplicateBoundaries(boundaries)

	// If no sections found, return entire text as one section
	if len(boundaries) == 0 {
		return []*Section{{
			Level:    0,
			Title:    "",
			Content:  text,
			StartPos: 0,
			EndPos:   len(text),
		}}
	}

	// Build sections
	var sections []*Section

	// Add content before first section if any
	if boundaries[0].pos > 0 {
		preamble := strings.TrimSpace(text[:boundaries[0].pos])
		if preamble != "" {
			sections = append(sections, &Section{
				Level:    0,
				Title:    "Introduction",
				Content:  preamble,
				StartPos: 0,
				EndPos:   boundaries[0].pos,
			})
		}
	}

	// Build sections from boundaries
	for i, b := range boundaries {
		endPos := len(text)
		if i+1 < len(boundaries) {
			endPos = boundaries[i+1].pos
		}

		content := strings.TrimSpace(text[b.pos:endPos])

		sections = append(sections, &Section{
			Number:   b.number,
			Level:    b.level,
			Title:    b.title,
			Content:  content,
			StartPos: b.pos,
			EndPos:   endPos,
		})
	}

	return sections
}

// chunkSection chunks a single section, respecting token limits.
func (c *SemanticChunker) chunkSection(section *Section, metadata ChunkMetadata, chunkIndex *int) []Chunk {
	content := section.Content
	tokenCount := c.countTokens(content)

	// If section fits in one chunk, return it directly
	if tokenCount <= c.config.MaxTokens {
		// Skip if too small
		if tokenCount < c.config.MinChunkSize && section.Level > 0 {
			// Return anyway but mark as small
		}

		chunk := c.createChunk(content, section, metadata, *chunkIndex)
		*chunkIndex++
		return []Chunk{chunk}
	}

	// Section is too large, split by paragraphs
	return c.splitByParagraphs(section, metadata, chunkIndex)
}

// splitByParagraphs splits a section by paragraphs.
func (c *SemanticChunker) splitByParagraphs(section *Section, metadata ChunkMetadata, chunkIndex *int) []Chunk {
	paragraphs := splitParagraphs(section.Content)
	var chunks []Chunk
	var currentText strings.Builder
	currentTokens := 0
	startPos := section.StartPos

	for _, para := range paragraphs {
		paraTokens := c.countTokens(para)

		// If single paragraph exceeds max, split by sentences
		if paraTokens > c.config.MaxTokens {
			// Flush current buffer first
			if currentText.Len() > 0 {
				chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
				chunks = append(chunks, chunk)
				*chunkIndex++
				currentText.Reset()
				currentTokens = 0
			}

			// Split paragraph by sentences
			sentenceChunks := c.splitBySentences(para, section, metadata, chunkIndex, startPos)
			chunks = append(chunks, sentenceChunks...)
			startPos += len(para) + 2 // +2 for paragraph break
			continue
		}

		// Check if adding this paragraph exceeds limit
		if currentTokens+paraTokens > c.config.MaxTokens {
			// Create chunk from current buffer
			if currentText.Len() > 0 {
				chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
				chunks = append(chunks, chunk)
				*chunkIndex++
				startPos += currentText.Len()
				currentText.Reset()
				currentTokens = 0
			}
		}

		// Add paragraph to current buffer
		if currentText.Len() > 0 {
			currentText.WriteString("\n\n")
		}
		currentText.WriteString(para)
		currentTokens += paraTokens
	}

	// Flush remaining content
	if currentText.Len() > 0 {
		chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
		chunks = append(chunks, chunk)
		*chunkIndex++
	}

	return chunks
}

// splitBySentences splits text by sentences when paragraphs are too large.
func (c *SemanticChunker) splitBySentences(text string, section *Section, metadata ChunkMetadata, chunkIndex *int, startPos int) []Chunk {
	sentences := splitSentences(text)
	var chunks []Chunk
	var currentText strings.Builder
	currentTokens := 0

	for _, sent := range sentences {
		sentTokens := c.countTokens(sent)

		// If single sentence exceeds max, split by words (rare case)
		if sentTokens > c.config.MaxTokens {
			// Flush current buffer
			if currentText.Len() > 0 {
				chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
				chunks = append(chunks, chunk)
				*chunkIndex++
				startPos += currentText.Len()
				currentText.Reset()
				currentTokens = 0
			}

			// Split by words as last resort
			wordChunks := c.splitByWords(sent, section, metadata, chunkIndex, startPos)
			chunks = append(chunks, wordChunks...)
			startPos += len(sent) + 1
			continue
		}

		// Check if adding sentence exceeds limit
		if currentTokens+sentTokens > c.config.MaxTokens {
			if currentText.Len() > 0 {
				chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
				chunks = append(chunks, chunk)
				*chunkIndex++
				startPos += currentText.Len()
				currentText.Reset()
				currentTokens = 0
			}
		}

		// Add sentence
		if currentText.Len() > 0 {
			currentText.WriteString(" ")
		}
		currentText.WriteString(sent)
		currentTokens += sentTokens
	}

	// Flush remaining
	if currentText.Len() > 0 {
		chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
		chunks = append(chunks, chunk)
		*chunkIndex++
	}

	return chunks
}

// splitByWords splits text by words (last resort for very long sentences).
func (c *SemanticChunker) splitByWords(text string, section *Section, metadata ChunkMetadata, chunkIndex *int, startPos int) []Chunk {
	words := strings.Fields(text)
	var chunks []Chunk
	var currentText strings.Builder
	currentTokens := 0

	for _, word := range words {
		wordTokens := c.countTokens(word)

		if currentTokens+wordTokens > c.config.MaxTokens && currentText.Len() > 0 {
			chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
			chunks = append(chunks, chunk)
			*chunkIndex++
			startPos += currentText.Len()
			currentText.Reset()
			currentTokens = 0
		}

		if currentText.Len() > 0 {
			currentText.WriteString(" ")
		}
		currentText.WriteString(word)
		currentTokens += wordTokens
	}

	if currentText.Len() > 0 {
		chunk := c.createChunkFromText(currentText.String(), section, metadata, *chunkIndex, startPos)
		chunks = append(chunks, chunk)
		*chunkIndex++
	}

	return chunks
}

// addOverlap adds overlap text from previous chunk to each chunk.
func (c *SemanticChunker) addOverlap(chunks []Chunk) []Chunk {
	if len(chunks) <= 1 || c.config.OverlapTokens == 0 {
		return chunks
	}

	for i := 1; i < len(chunks); i++ {
		prevContent := chunks[i-1].Content
		overlapText := c.getOverlapText(prevContent)

		if overlapText != "" && !strings.HasPrefix(chunks[i].Content, overlapText) {
			// Prepend overlap text
			newContent := overlapText + " " + chunks[i].Content
			chunks[i].Content = newContent
			chunks[i].TokenCount = c.countTokens(newContent)
		}
	}

	return chunks
}

// getOverlapText extracts the last N tokens worth of text for overlap.
func (c *SemanticChunker) getOverlapText(text string) string {
	tokens := c.tokenizer.Encode(text, nil, nil)

	if len(tokens) <= c.config.OverlapTokens {
		return ""
	}

	// Get last N tokens
	overlapTokens := tokens[len(tokens)-c.config.OverlapTokens:]
	overlapText := c.tokenizer.Decode(overlapTokens)

	// Clean up - try to start at a word boundary
	overlapText = strings.TrimSpace(overlapText)
	if idx := strings.Index(overlapText, " "); idx > 0 && idx < len(overlapText)/2 {
		overlapText = overlapText[idx+1:]
	}

	return strings.TrimSpace(overlapText)
}

// createChunk creates a chunk from section content.
func (c *SemanticChunker) createChunk(content string, section *Section, metadata ChunkMetadata, index int) Chunk {
	hierarchy := buildSectionHierarchy(section)

	chunk := Chunk{
		ID:               uuid.New().String(),
		Content:          content,
		TokenCount:       c.countTokens(content),
		PageNumber:       metadata.PageNumber,
		SectionTitle:     section.Title,
		SectionHierarchy: hierarchy,
		ChunkIndex:       index,
		StartChar:        section.StartPos,
		EndChar:          section.EndPos,
		Metadata:         make(map[string]interface{}),
	}

	// Add metadata
	chunk.Metadata["document_id"] = metadata.DocumentID
	chunk.Metadata["source_type"] = metadata.SourceType
	chunk.Metadata["document_title"] = metadata.DocumentTitle

	if metadata.Extra != nil {
		for k, v := range metadata.Extra {
			chunk.Metadata[k] = v
		}
	}

	return chunk
}

// createChunkFromText creates a chunk from arbitrary text.
func (c *SemanticChunker) createChunkFromText(content string, section *Section, metadata ChunkMetadata, index int, startPos int) Chunk {
	hierarchy := buildSectionHierarchy(section)

	chunk := Chunk{
		ID:               uuid.New().String(),
		Content:          content,
		TokenCount:       c.countTokens(content),
		PageNumber:       metadata.PageNumber,
		SectionTitle:     section.Title,
		SectionHierarchy: hierarchy,
		ChunkIndex:       index,
		StartChar:        startPos,
		EndChar:          startPos + len(content),
		Metadata:         make(map[string]interface{}),
	}

	chunk.Metadata["document_id"] = metadata.DocumentID
	chunk.Metadata["source_type"] = metadata.SourceType
	chunk.Metadata["document_title"] = metadata.DocumentTitle

	if metadata.Extra != nil {
		for k, v := range metadata.Extra {
			chunk.Metadata[k] = v
		}
	}

	return chunk
}

// countTokens counts the number of tokens in text.
func (c *SemanticChunker) countTokens(text string) int {
	tokens := c.tokenizer.Encode(text, nil, nil)
	return len(tokens)
}

// CountTokens is a public method to count tokens.
func (c *SemanticChunker) CountTokens(text string) int {
	return c.countTokens(text)
}

// Helper functions

// splitParagraphs splits text into paragraphs.
func splitParagraphs(text string) []string {
	// Split on double newlines or more
	re := regexp.MustCompile(`\n\s*\n`)
	parts := re.Split(text, -1)

	var paragraphs []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}

	return paragraphs
}

// splitSentences splits text into sentences.
func splitSentences(text string) []string {
	// Handle common abbreviations that shouldn't split
	text = protectAbbreviations(text)

	// Split on sentence-ending punctuation
	re := regexp.MustCompile(`([.!?]+)\s+`)
	parts := re.Split(text, -1)

	var sentences []string
	matches := re.FindAllStringSubmatch(text, -1)
	matchIdx := 0

	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Restore the punctuation
		if matchIdx < len(matches) && i < len(parts)-1 {
			part += matches[matchIdx][1]
			matchIdx++
		}

		// Restore abbreviations
		part = restoreAbbreviations(part)
		sentences = append(sentences, part)
	}

	return sentences
}

// protectAbbreviations replaces periods in common abbreviations.
func protectAbbreviations(text string) string {
	abbrevs := []string{
		"Mr.", "Mrs.", "Ms.", "Dr.", "Prof.", "Sr.", "Jr.",
		"Inc.", "Ltd.", "Corp.", "Co.",
		"e.g.", "i.e.", "etc.", "vs.",
		"No.", "Vol.", "Art.", "Sec.",
	}

	for _, abbr := range abbrevs {
		protected := strings.ReplaceAll(abbr, ".", "<DOT>")
		text = strings.ReplaceAll(text, abbr, protected)
	}

	return text
}

// restoreAbbreviations restores protected abbreviations.
func restoreAbbreviations(text string) string {
	return strings.ReplaceAll(text, "<DOT>", ".")
}

// buildSectionHierarchy builds a hierarchy path for a section.
func buildSectionHierarchy(section *Section) []string {
	var hierarchy []string

	current := section
	for current != nil {
		if current.Title != "" {
			hierarchy = append([]string{current.Title}, hierarchy...)
		}
		current = current.Parent
	}

	return hierarchy
}

// sortBoundaries sorts section boundaries by position.
func sortBoundaries(boundaries []struct {
	pos    int
	level  int
	title  string
	number string
}) {
	for i := 0; i < len(boundaries)-1; i++ {
		for j := 0; j < len(boundaries)-i-1; j++ {
			if boundaries[j].pos > boundaries[j+1].pos {
				boundaries[j], boundaries[j+1] = boundaries[j+1], boundaries[j]
			}
		}
	}
}

// deduplicateBoundaries removes boundaries at the same position.
func deduplicateBoundaries(boundaries []struct {
	pos    int
	level  int
	title  string
	number string
}) []struct {
	pos    int
	level  int
	title  string
	number string
} {
	if len(boundaries) == 0 {
		return boundaries
	}

	result := []struct {
		pos    int
		level  int
		title  string
		number string
	}{boundaries[0]}

	for i := 1; i < len(boundaries); i++ {
		if boundaries[i].pos != result[len(result)-1].pos {
			result = append(result, boundaries[i])
		}
	}

	return result
}

// IsArabicOrMalay checks if text contains Arabic or Malay characters.
func IsArabicOrMalay(text string) bool {
	for _, r := range text {
		// Arabic Unicode range
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
		// Arabic Supplement
		if r >= 0x0750 && r <= 0x077F {
			return true
		}
		// Arabic Extended-A
		if r >= 0x08A0 && r <= 0x08FF {
			return true
		}
	}
	return false
}

// NormalizeText normalizes text for consistent processing.
func NormalizeText(text string) string {
	// Normalize Unicode
	text = strings.ToValidUTF8(text, "")

	// Normalize whitespace
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, text)

	// Remove excessive spaces
	re := regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// SimpleChunk provides a simple chunking method without section awareness.
func SimpleChunk(text string, maxTokens, overlapTokens int) ([]string, error) {
	chunker, err := NewSemanticChunker(ChunkerConfig{
		MaxTokens:     maxTokens,
		OverlapTokens: overlapTokens,
		MinChunkSize:  50,
	})
	if err != nil {
		return nil, err
	}

	chunks := chunker.Chunk(text, ChunkMetadata{})
	result := make([]string, len(chunks))
	for i, chunk := range chunks {
		result[i] = chunk.Content
	}

	return result, nil
}
