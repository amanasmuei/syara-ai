package visual

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/google/uuid"
)

// Citation represents a complete visual citation for a retrieved chunk.
type Citation struct {
	// ID is the unique identifier for this citation
	ID string `json:"id"`
	// ChunkID references the source chunk
	ChunkID string `json:"chunk_id"`
	// ContentSnippet is a shortened version of the chunk content
	ContentSnippet string `json:"content_snippet"`
	// Source contains information about the source document
	Source SourceInfo `json:"source"`
	// Visual contains URLs to visual representations
	Visual VisualCitation `json:"visual"`
	// Download contains links for downloading source materials
	Download DownloadLinks `json:"download"`
	// Relevance is the similarity score (0-1)
	Relevance float64 `json:"relevance"`
	// CitationNumber is the display number (1, 2, 3, etc.)
	CitationNumber int `json:"citation_number"`
	// CreatedAt is when this citation was generated
	CreatedAt time.Time `json:"created_at"`
}

// SourceInfo contains metadata about the citation source.
type SourceInfo struct {
	// Type is the source type (e.g., 'bnm', 'aaoifi')
	Type string `json:"type"`
	// Document is the document title
	Document string `json:"document"`
	// Section is the section title within the document
	Section string `json:"section,omitempty"`
	// Page is the page number (1-indexed)
	Page int `json:"page"`
	// StandardNumber is the regulatory standard number (if applicable)
	StandardNumber string `json:"standard_number,omitempty"`
	// Category is the document category
	Category string `json:"category,omitempty"`
	// EffectiveDate is when the document became effective
	EffectiveDate *time.Time `json:"effective_date,omitempty"`
}

// VisualCitation contains URLs to visual representations of the citation.
type VisualCitation struct {
	// ThumbnailURL is the URL to a small thumbnail of the page
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	// FullImageURL is the URL to the full-resolution page image
	FullImageURL string `json:"full_image_url,omitempty"`
	// HighlightedURL is the URL to the page with the citation highlighted
	HighlightedURL string `json:"highlighted_url,omitempty"`
	// CroppedURL is the URL to a cropped image of just the cited region
	CroppedURL string `json:"cropped_url,omitempty"`
	// HighlightRegion is the bounding box of the highlighted region
	HighlightRegion *BoundingBox `json:"highlight_region,omitempty"`
}

// DownloadLinks contains URLs for downloading source materials.
type DownloadLinks struct {
	// OriginalPDF is the URL to the complete original PDF
	OriginalPDF string `json:"original_pdf,omitempty"`
	// PagePDF is the URL to a single-page PDF containing just the cited page
	PagePDF string `json:"page_pdf,omitempty"`
	// DirectLink is a direct link to the document (e.g., BNM website)
	DirectLink string `json:"direct_link,omitempty"`
	// SourceURL is the original source URL where the document was obtained
	SourceURL string `json:"source_url,omitempty"`
}

// CitationBuilder builds visual citations from retrieved chunks.
type CitationBuilder struct {
	storage     storage.ObjectStorage
	highlighter *Highlighter
	cropper     *Cropper
	extractor   *PDFExtractor
	log         *logger.Logger
	config      CitationBuilderConfig
}

// CitationBuilderConfig configures the citation builder behavior.
type CitationBuilderConfig struct {
	// SnippetMaxLength is the maximum length for content snippets (default: 300)
	SnippetMaxLength int
	// GenerateHighlights enables highlight image generation
	GenerateHighlights bool
	// GenerateCrops enables cropped image generation
	GenerateCrops bool
	// GeneratePagePDFs enables single-page PDF extraction
	GeneratePagePDFs bool
	// CropPadding is the padding around cropped regions (default: 20)
	CropPadding int
	// SignedURLExpiry is the duration for signed URLs (default: 24h)
	SignedURLExpiry time.Duration
}

// DefaultCitationBuilderConfig returns sensible defaults.
func DefaultCitationBuilderConfig() CitationBuilderConfig {
	return CitationBuilderConfig{
		SnippetMaxLength:   300,
		GenerateHighlights: true,
		GenerateCrops:      true,
		GeneratePagePDFs:   true,
		CropPadding:        20,
		SignedURLExpiry:    24 * time.Hour,
	}
}

// NewCitationBuilder creates a new CitationBuilder instance.
func NewCitationBuilder(
	store storage.ObjectStorage,
	log *logger.Logger,
	config CitationBuilderConfig,
) *CitationBuilder {
	if log == nil {
		log = logger.Default()
	}

	return &CitationBuilder{
		storage:     store,
		highlighter: NewHighlighter(store, log),
		cropper:     NewCropper(store, log),
		extractor:   NewPDFExtractor(store, log),
		log:         log.WithComponent("citation-builder"),
		config:      config,
	}
}

// BuildCitations generates citations for a list of retrieved chunks.
func (cb *CitationBuilder) BuildCitations(
	ctx context.Context,
	chunks []storage.RetrievedChunk,
	queryID string,
) ([]Citation, error) {
	if len(chunks) == 0 {
		return []Citation{}, nil
	}

	cb.log.Info("building citations",
		"chunk_count", len(chunks),
		"query_id", queryID,
	)

	startTime := time.Now()
	citations := make([]Citation, 0, len(chunks))

	// Group chunks by page for efficient batch processing
	pageGroups := cb.groupChunksByPage(chunks)

	cb.log.Debug("chunks grouped by page",
		"page_count", len(pageGroups),
	)

	// Process each page group
	citationNum := 1
	for pageKey, pageChunks := range pageGroups {
		pageCitations, err := cb.processPageGroup(ctx, pageKey, pageChunks, queryID, citationNum)
		if err != nil {
			cb.log.WithError(err).Warn("failed to process page group",
				"page_key", pageKey,
			)
			// Continue with other pages instead of failing completely
			continue
		}

		citations = append(citations, pageCitations...)
		citationNum += len(pageCitations)
	}

	// Sort citations by relevance (descending)
	sort.Slice(citations, func(i, j int) bool {
		return citations[i].Relevance > citations[j].Relevance
	})

	// Re-number citations after sorting
	for i := range citations {
		citations[i].CitationNumber = i + 1
	}

	cb.log.Info("citations built successfully",
		"citation_count", len(citations),
		"duration_ms", time.Since(startTime).Milliseconds(),
	)

	return citations, nil
}

// BuildSingleCitation generates a citation for a single chunk.
func (cb *CitationBuilder) BuildSingleCitation(
	ctx context.Context,
	chunk storage.RetrievedChunk,
	queryID string,
	citationNum int,
) (*Citation, error) {
	cb.log.Debug("building single citation",
		"chunk_id", chunk.ID.String(),
		"citation_num", citationNum,
	)

	citation := &Citation{
		ID:             uuid.New().String(),
		ChunkID:        chunk.ID.String(),
		ContentSnippet: cb.createSnippet(chunk.Content),
		Source:         cb.buildSourceInfo(chunk),
		Relevance:      chunk.Similarity,
		CitationNumber: citationNum,
		CreatedAt:      time.Now().UTC(),
	}

	// Build visual citation
	visual, err := cb.buildVisualCitation(ctx, chunk, queryID, citationNum)
	if err != nil {
		cb.log.WithError(err).Warn("failed to build visual citation",
			"chunk_id", chunk.ID.String(),
		)
		// Continue with partial visual data
	}
	citation.Visual = visual

	// Build download links
	citation.Download = cb.buildDownloadLinks(ctx, chunk, queryID)

	return citation, nil
}

// pageKey uniquely identifies a page within a document.
type pageKey struct {
	DocumentID string
	PageNumber int
	ImagePath  string
}

// groupChunksByPage groups chunks by their page for batch processing.
func (cb *CitationBuilder) groupChunksByPage(chunks []storage.RetrievedChunk) map[pageKey][]storage.RetrievedChunk {
	groups := make(map[pageKey][]storage.RetrievedChunk)

	for _, chunk := range chunks {
		key := pageKey{
			DocumentID: chunk.DocumentID.String(),
			PageNumber: chunk.PageNumber,
			ImagePath:  chunk.PageImagePath,
		}
		groups[key] = append(groups[key], chunk)
	}

	return groups
}

// processPageGroup processes all chunks on a single page.
func (cb *CitationBuilder) processPageGroup(
	ctx context.Context,
	key pageKey,
	chunks []storage.RetrievedChunk,
	queryID string,
	startCitationNum int,
) ([]Citation, error) {
	citations := make([]Citation, 0, len(chunks))

	// If we have multiple chunks on the same page and highlights are enabled,
	// create a combined highlighted image
	var combinedHighlightURL string
	if cb.config.GenerateHighlights && key.ImagePath != "" && len(chunks) > 1 {
		regions := make([]RegionWithLabel, len(chunks))
		for i, chunk := range chunks {
			bbox := cb.extractBoundingBox(chunk)
			if bbox != nil {
				regions[i] = RegionWithLabel{
					BoundingBox: *bbox,
					Label:       fmt.Sprintf("%d", startCitationNum+i),
				}
			}
		}

		// Generate combined highlight
		result, err := cb.highlighter.HighlightMultipleRegions(ctx, key.ImagePath, regions, queryID)
		if err != nil {
			cb.log.WithError(err).Warn("failed to create combined highlight")
		} else {
			combinedHighlightURL = result.ImageURL
		}
	}

	// Build citation for each chunk
	for i, chunk := range chunks {
		citationNum := startCitationNum + i
		citation, err := cb.buildChunkCitation(ctx, chunk, queryID, citationNum, combinedHighlightURL)
		if err != nil {
			cb.log.WithError(err).Warn("failed to build chunk citation",
				"chunk_id", chunk.ID.String(),
			)
			continue
		}
		citations = append(citations, *citation)
	}

	return citations, nil
}

// buildChunkCitation builds a citation for a single chunk.
func (cb *CitationBuilder) buildChunkCitation(
	ctx context.Context,
	chunk storage.RetrievedChunk,
	queryID string,
	citationNum int,
	combinedHighlightURL string,
) (*Citation, error) {
	citation := &Citation{
		ID:             uuid.New().String(),
		ChunkID:        chunk.ID.String(),
		ContentSnippet: cb.createSnippet(chunk.Content),
		Source:         cb.buildSourceInfo(chunk),
		Relevance:      chunk.Similarity,
		CitationNumber: citationNum,
		CreatedAt:      time.Now().UTC(),
	}

	// Build visual citation
	visual := VisualCitation{}

	// Set thumbnail and full image URLs from chunk data
	if chunk.ThumbnailPath != "" {
		url, err := cb.storage.GetURL(ctx, chunk.ThumbnailPath)
		if err == nil {
			visual.ThumbnailURL = url
		}
	}

	if chunk.PageImagePath != "" {
		url, err := cb.storage.GetURL(ctx, chunk.PageImagePath)
		if err == nil {
			visual.FullImageURL = url
		}
	}

	// Extract bounding box
	bbox := cb.extractBoundingBox(chunk)
	if bbox != nil {
		visual.HighlightRegion = bbox

		// Generate individual highlight if not using combined
		if cb.config.GenerateHighlights && chunk.PageImagePath != "" && combinedHighlightURL == "" {
			opts := DefaultHighlightOptions()
			opts.LabelText = fmt.Sprintf("%d", citationNum)
			result, err := cb.highlighter.HighlightRegion(
				ctx,
				chunk.PageImagePath,
				*bbox,
				opts,
				queryID,
				chunk.ID.String(),
			)
			if err == nil {
				visual.HighlightedURL = result.ImageURL
			}
		} else if combinedHighlightURL != "" {
			visual.HighlightedURL = combinedHighlightURL
		}

		// Generate cropped image
		if cb.config.GenerateCrops && chunk.PageImagePath != "" {
			cropOpts := DefaultCropOptions()
			cropOpts.Padding = cb.config.CropPadding
			cropOpts.AddHighlight = true
			result, err := cb.cropper.CropRegion(
				ctx,
				chunk.PageImagePath,
				*bbox,
				cropOpts,
				queryID,
				chunk.ID.String(),
			)
			if err == nil {
				visual.CroppedURL = result.ImageURL
			}
		}
	}

	citation.Visual = visual

	// Build download links
	citation.Download = cb.buildDownloadLinks(ctx, chunk, queryID)

	return citation, nil
}

// buildSourceInfo creates source information from a chunk.
func (cb *CitationBuilder) buildSourceInfo(chunk storage.RetrievedChunk) SourceInfo {
	info := SourceInfo{
		Type:           chunk.SourceType,
		Document:       chunk.DocumentTitle,
		Section:        chunk.SectionTitle,
		Page:           chunk.PageNumber,
		StandardNumber: chunk.StandardNum,
		Category:       chunk.Category,
	}

	if !chunk.EffectiveDate.IsZero() {
		info.EffectiveDate = &chunk.EffectiveDate
	}

	return info
}

// buildVisualCitation builds visual citation data for a chunk.
func (cb *CitationBuilder) buildVisualCitation(
	ctx context.Context,
	chunk storage.RetrievedChunk,
	queryID string,
	citationNum int,
) (VisualCitation, error) {
	visual := VisualCitation{}

	// Get thumbnail URL
	if chunk.ThumbnailPath != "" {
		url, err := cb.storage.GetURL(ctx, chunk.ThumbnailPath)
		if err == nil {
			visual.ThumbnailURL = url
		}
	}

	// Get full image URL
	if chunk.PageImagePath != "" {
		url, err := cb.storage.GetURL(ctx, chunk.PageImagePath)
		if err == nil {
			visual.FullImageURL = url
		}
	}

	// Extract and process bounding box
	bbox := cb.extractBoundingBox(chunk)
	if bbox != nil {
		visual.HighlightRegion = bbox

		// Generate highlighted image
		if cb.config.GenerateHighlights && chunk.PageImagePath != "" {
			opts := DefaultHighlightOptions()
			opts.LabelText = fmt.Sprintf("%d", citationNum)
			result, err := cb.highlighter.HighlightRegion(
				ctx,
				chunk.PageImagePath,
				*bbox,
				opts,
				queryID,
				chunk.ID.String(),
			)
			if err != nil {
				cb.log.WithError(err).Warn("failed to generate highlight")
			} else {
				visual.HighlightedURL = result.ImageURL
			}
		}

		// Generate cropped image
		if cb.config.GenerateCrops && chunk.PageImagePath != "" {
			cropOpts := DefaultCropOptions()
			cropOpts.Padding = cb.config.CropPadding
			cropOpts.AddHighlight = true
			result, err := cb.cropper.CropRegion(
				ctx,
				chunk.PageImagePath,
				*bbox,
				cropOpts,
				queryID,
				chunk.ID.String(),
			)
			if err != nil {
				cb.log.WithError(err).Warn("failed to generate crop")
			} else {
				visual.CroppedURL = result.ImageURL
			}
		}
	}

	return visual, nil
}

// buildDownloadLinks creates download links for a chunk.
func (cb *CitationBuilder) buildDownloadLinks(
	ctx context.Context,
	chunk storage.RetrievedChunk,
	queryID string,
) DownloadLinks {
	links := DownloadLinks{}

	// Get original PDF link
	originalPath := storage.BuildOriginalPath(chunk.SourceType, chunk.DocumentID.String()+".pdf")
	url, err := cb.storage.GetURL(ctx, originalPath)
	if err == nil {
		links.OriginalPDF = url
	}

	// Generate single-page PDF if configured
	if cb.config.GeneratePagePDFs && chunk.PageNumber > 0 {
		pagePath := BuildPagePDFPath(chunk.DocumentID.String(), chunk.PageNumber)
		// Check if it exists or generate it
		exists, _ := cb.storage.Exists(ctx, pagePath)
		if exists {
			url, err := cb.storage.GetURL(ctx, pagePath)
			if err == nil {
				links.PagePDF = url
			}
		}
		// Note: Actual PDF extraction would be done lazily on-demand
		// to avoid processing overhead for every citation
	}

	// Extract source URL from chunk metadata if available
	if chunk.Metadata != nil {
		var meta map[string]interface{}
		if err := json.Unmarshal(chunk.Metadata, &meta); err == nil {
			if sourceURL, ok := meta["source_url"].(string); ok {
				links.SourceURL = sourceURL
			}
			if directLink, ok := meta["direct_link"].(string); ok {
				links.DirectLink = directLink
			}
		}
	}

	return links
}

// extractBoundingBox extracts bounding box from chunk data.
func (cb *CitationBuilder) extractBoundingBox(chunk storage.RetrievedChunk) *BoundingBox {
	if chunk.BoundingBox == nil {
		return nil
	}

	var bbox BoundingBox
	if err := json.Unmarshal(chunk.BoundingBox, &bbox); err != nil {
		cb.log.WithError(err).Warn("failed to unmarshal bounding box")
		return nil
	}

	// Validate bounding box
	if bbox.Width <= 0 || bbox.Height <= 0 {
		return nil
	}

	return &bbox
}

// createSnippet creates a shortened snippet from content.
func (cb *CitationBuilder) createSnippet(content string) string {
	maxLen := cb.config.SnippetMaxLength
	if maxLen <= 0 {
		maxLen = 300
	}

	// Clean up the content
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\t", " ")

	// Collapse multiple spaces
	for strings.Contains(content, "  ") {
		content = strings.ReplaceAll(content, "  ", " ")
	}

	// Truncate if necessary
	if len(content) > maxLen {
		// Try to break at a word boundary
		truncated := content[:maxLen]
		lastSpace := strings.LastIndex(truncated, " ")
		if lastSpace > maxLen/2 {
			truncated = truncated[:lastSpace]
		}
		content = truncated + "..."
	}

	return content
}

// GeneratePagePDF generates a single-page PDF on demand.
func (cb *CitationBuilder) GeneratePagePDF(
	ctx context.Context,
	originalPDFPath string,
	pageNum int,
	documentID string,
) (string, error) {
	cb.log.Info("generating page PDF",
		"source", originalPDFPath,
		"page", pageNum,
	)

	result, err := cb.extractor.ExtractPage(
		ctx,
		originalPDFPath,
		pageNum,
		ExtractPageOptions{
			ExtractPDF:   true,
			ExtractImage: false,
		},
		documentID,
	)
	if err != nil {
		return "", fmt.Errorf("failed to extract page: %w", err)
	}

	return result.PDFURL, nil
}

// GetCitationByID retrieves a citation by its ID from a list.
func GetCitationByID(citations []Citation, id string) *Citation {
	for i := range citations {
		if citations[i].ID == id {
			return &citations[i]
		}
	}
	return nil
}

// GetCitationByChunkID retrieves a citation by its chunk ID from a list.
func GetCitationByChunkID(citations []Citation, chunkID string) *Citation {
	for i := range citations {
		if citations[i].ChunkID == chunkID {
			return &citations[i]
		}
	}
	return nil
}

// SortCitationsByRelevance sorts citations by relevance score (descending).
func SortCitationsByRelevance(citations []Citation) {
	sort.Slice(citations, func(i, j int) bool {
		return citations[i].Relevance > citations[j].Relevance
	})
}

// SortCitationsByPage sorts citations by page number (ascending).
func SortCitationsByPage(citations []Citation) {
	sort.Slice(citations, func(i, j int) bool {
		if citations[i].Source.Document != citations[j].Source.Document {
			return citations[i].Source.Document < citations[j].Source.Document
		}
		return citations[i].Source.Page < citations[j].Source.Page
	})
}

// FilterCitationsBySource filters citations by source type.
func FilterCitationsBySource(citations []Citation, sourceType string) []Citation {
	filtered := make([]Citation, 0)
	for _, c := range citations {
		if c.Source.Type == sourceType {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// FilterCitationsByMinRelevance filters citations by minimum relevance score.
func FilterCitationsByMinRelevance(citations []Citation, minRelevance float64) []Citation {
	filtered := make([]Citation, 0)
	for _, c := range citations {
		if c.Relevance >= minRelevance {
			filtered = append(filtered, c)
		}
	}
	return filtered
}
