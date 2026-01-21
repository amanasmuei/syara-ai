// Package processor provides document processing functionality including PDF text extraction.
package processor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/gen2brain/go-fitz"
	"github.com/google/uuid"
	"github.com/nfnt/resize"
)

// PDFProcessorConfig holds configuration for PDF processing.
type PDFProcessorConfig struct {
	RenderDPI       int  // DPI for page rendering (default: 300)
	ThumbnailWidth  uint // Thumbnail width in pixels (default: 200)
	MaxConcurrency  int  // Max concurrent page processing (default: 4)
	UploadImages    bool // Whether to upload images to storage
	ExtractImages   bool // Whether to extract page images
	KeepLocalCopies bool // Whether to keep local copies of images
}

// DefaultPDFProcessorConfig returns default configuration.
func DefaultPDFProcessorConfig() PDFProcessorConfig {
	return PDFProcessorConfig{
		RenderDPI:       300,
		ThumbnailWidth:  200,
		MaxConcurrency:  4,
		UploadImages:    true,
		ExtractImages:   true,
		KeepLocalCopies: false,
	}
}

// DocumentMetadata holds metadata about a document being processed.
type DocumentMetadata struct {
	ID             string                 `json:"id"`
	SourceType     string                 `json:"source_type"`
	Title          string                 `json:"title"`
	FileName       string                 `json:"file_name"`
	OriginalURL    string                 `json:"original_url"`
	Category       string                 `json:"category"`
	StandardNumber string                 `json:"standard_number"`
	Author         string                 `json:"author"`
	Subject        string                 `json:"subject"`
	Keywords       []string               `json:"keywords"`
	CreationDate   time.Time              `json:"creation_date"`
	ModDate        time.Time              `json:"mod_date"`
	Extra          map[string]interface{} `json:"extra"`
}

// BoundingBox represents a rectangular region on a page.
type BoundingBox struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	PageWidth  float64 `json:"page_width"`
	PageHeight float64 `json:"page_height"`
}

// TextBlock represents a block of text with its position.
type TextBlock struct {
	Text        string      `json:"text"`
	BoundingBox BoundingBox `json:"bounding_box"`
	PageNumber  int         `json:"page_number"`
	FontName    string      `json:"font_name,omitempty"`
	FontSize    float64     `json:"font_size,omitempty"`
}

// ProcessedPage represents a processed PDF page.
type ProcessedPage struct {
	PageNumber    int         `json:"page_number"`
	Text          string      `json:"text"`
	ImagePath     string      `json:"image_path"`
	ImageURL      string      `json:"image_url"`
	ThumbnailPath string      `json:"thumbnail_path"`
	ThumbnailURL  string      `json:"thumbnail_url"`
	Width         int         `json:"width"`
	Height        int         `json:"height"`
	TextBlocks    []TextBlock `json:"text_blocks"`
}

// ProcessedDocument represents a fully processed PDF document.
type ProcessedDocument struct {
	ID          string           `json:"id"`
	Metadata    DocumentMetadata `json:"metadata"`
	Pages       []ProcessedPage  `json:"pages"`
	TotalPages  int              `json:"total_pages"`
	ContentHash string           `json:"content_hash"`
	ProcessedAt time.Time        `json:"processed_at"`
}

// PDFProcessor processes PDF documents for text extraction and page rendering.
type PDFProcessor struct {
	config  PDFProcessorConfig
	storage storage.ObjectStorage
	log     *logger.Logger
}

// NewPDFProcessor creates a new PDF processor instance.
func NewPDFProcessor(cfg PDFProcessorConfig, store storage.ObjectStorage, log *logger.Logger) *PDFProcessor {
	if log == nil {
		log = logger.Default()
	}

	return &PDFProcessor{
		config:  cfg,
		storage: store,
		log:     log.WithComponent("pdf-processor"),
	}
}

// ProcessPDF processes a PDF file from the given path.
func (p *PDFProcessor) ProcessPDF(ctx context.Context, pdfPath string, metadata DocumentMetadata) (*ProcessedDocument, error) {
	p.log.Info("processing PDF", "path", pdfPath, "doc_id", metadata.ID)

	// Open the PDF
	doc, err := fitz.New(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF: %w", err)
	}
	defer doc.Close()

	// Generate document ID if not provided
	if metadata.ID == "" {
		metadata.ID = uuid.New().String()
	}

	// Extract PDF metadata
	if err := p.extractPDFMetadata(doc, &metadata); err != nil {
		p.log.WithError(err).Warn("failed to extract PDF metadata")
	}

	// Calculate content hash
	contentHash, err := p.calculateFileHash(pdfPath)
	if err != nil {
		p.log.WithError(err).Warn("failed to calculate content hash")
	}

	totalPages := doc.NumPage()
	p.log.Info("PDF opened successfully", "total_pages", totalPages, "doc_id", metadata.ID)

	// Process pages concurrently
	pages, err := p.processPages(ctx, doc, metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to process pages: %w", err)
	}

	result := &ProcessedDocument{
		ID:          metadata.ID,
		Metadata:    metadata,
		Pages:       pages,
		TotalPages:  totalPages,
		ContentHash: contentHash,
		ProcessedAt: time.Now().UTC(),
	}

	p.log.Info("PDF processed successfully", "doc_id", metadata.ID, "pages", totalPages)
	return result, nil
}

// ProcessPDFBytes processes a PDF from byte data.
func (p *PDFProcessor) ProcessPDFBytes(ctx context.Context, data []byte, metadata DocumentMetadata) (*ProcessedDocument, error) {
	// Write to temporary file
	tmpFile, err := os.CreateTemp("", "pdf-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := tmpFile.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	return p.ProcessPDF(ctx, tmpFile.Name(), metadata)
}

// processPages processes all pages of the PDF sequentially.
// Note: Sequential processing is used because the fitz library may not be thread-safe
// for concurrent page access on the same document.
func (p *PDFProcessor) processPages(ctx context.Context, doc *fitz.Document, metadata DocumentMetadata) ([]ProcessedPage, error) {
	totalPages := doc.NumPage()
	if totalPages == 0 {
		return nil, nil
	}

	pages := make([]ProcessedPage, totalPages)
	var errs []error

	for i := 0; i < totalPages; i++ {
		// Check context before processing each page
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		page, err := p.processPage(ctx, doc, i, metadata)
		if err != nil {
			errs = append(errs, fmt.Errorf("page %d: %w", i+1, err))
			continue
		}
		pages[i] = page
	}

	if len(errs) > 0 {
		p.log.Warn("some pages failed to process", "error_count", len(errs))
	}

	return pages, nil
}

// processPage processes a single PDF page.
func (p *PDFProcessor) processPage(ctx context.Context, doc *fitz.Document, pageIdx int, metadata DocumentMetadata) (ProcessedPage, error) {
	pageNum := pageIdx + 1
	p.log.Debug("processing page", "page", pageNum, "doc_id", metadata.ID)

	result := ProcessedPage{
		PageNumber: pageNum,
	}

	// Extract text
	text, err := doc.Text(pageIdx)
	if err != nil {
		return result, fmt.Errorf("failed to extract text: %w", err)
	}
	result.Text = cleanText(text)

	// Render page image if configured
	if p.config.ExtractImages {
		img, err := doc.Image(pageIdx)
		if err != nil {
			p.log.WithError(err).Warn("failed to render page image", "page", pageNum)
		} else {
			result.Width = img.Bounds().Dx()
			result.Height = img.Bounds().Dy()

			// Upload page image
			if p.config.UploadImages && p.storage != nil {
				imagePath, imageURL, err := p.uploadPageImage(ctx, img, metadata, pageNum)
				if err != nil {
					p.log.WithError(err).Warn("failed to upload page image", "page", pageNum)
				} else {
					result.ImagePath = imagePath
					result.ImageURL = imageURL
				}

				// Generate and upload thumbnail
				thumbPath, thumbURL, err := p.uploadThumbnail(ctx, img, metadata, pageNum)
				if err != nil {
					p.log.WithError(err).Warn("failed to upload thumbnail", "page", pageNum)
				} else {
					result.ThumbnailPath = thumbPath
					result.ThumbnailURL = thumbURL
				}
			}
		}
	}

	return result, nil
}

// extractPDFMetadata extracts metadata from PDF document.
func (p *PDFProcessor) extractPDFMetadata(doc *fitz.Document, metadata *DocumentMetadata) error {
	// Get PDF metadata using go-fitz
	pdfMetadata := doc.Metadata()

	if metadata.Title == "" {
		if title, ok := pdfMetadata["title"]; ok {
			metadata.Title = title
		}
	}

	if metadata.Author == "" {
		if author, ok := pdfMetadata["author"]; ok {
			metadata.Author = author
		}
	}

	if metadata.Subject == "" {
		if subject, ok := pdfMetadata["subject"]; ok {
			metadata.Subject = subject
		}
	}

	if keywords, ok := pdfMetadata["keywords"]; ok && len(metadata.Keywords) == 0 {
		metadata.Keywords = parseKeywords(keywords)
	}

	// Parse dates
	if creationDate, ok := pdfMetadata["creationDate"]; ok {
		if t := parsePDFDate(creationDate); !t.IsZero() {
			metadata.CreationDate = t
		}
	}

	if modDate, ok := pdfMetadata["modDate"]; ok {
		if t := parsePDFDate(modDate); !t.IsZero() {
			metadata.ModDate = t
		}
	}

	// Extract standard number from title if applicable
	if metadata.StandardNumber == "" && metadata.Title != "" {
		metadata.StandardNumber = extractStandardNumber(metadata.Title)
	}

	return nil
}

// uploadPageImage uploads a page image to storage.
func (p *PDFProcessor) uploadPageImage(ctx context.Context, img image.Image, metadata DocumentMetadata, pageNum int) (string, string, error) {
	// Encode image to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", fmt.Errorf("failed to encode image: %w", err)
	}

	// Build storage path
	path := storage.BuildPagePath(metadata.SourceType, metadata.ID, pageNum)

	// Upload to storage
	storedPath, err := p.storage.UploadBytes(ctx, buf.Bytes(), path, "image/png")
	if err != nil {
		return "", "", fmt.Errorf("failed to upload image: %w", err)
	}

	// Get URL
	url, err := p.storage.GetURL(ctx, storedPath)
	if err != nil {
		url = "" // Non-fatal, just log warning
		p.log.WithError(err).Warn("failed to get image URL")
	}

	return storedPath, url, nil
}

// uploadThumbnail generates and uploads a thumbnail image.
func (p *PDFProcessor) uploadThumbnail(ctx context.Context, img image.Image, metadata DocumentMetadata, pageNum int) (string, string, error) {
	// Resize image for thumbnail
	thumb := resize.Resize(p.config.ThumbnailWidth, 0, img, resize.Lanczos3)

	// Encode thumbnail to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, thumb); err != nil {
		return "", "", fmt.Errorf("failed to encode thumbnail: %w", err)
	}

	// Build storage path
	path := storage.BuildThumbPath(metadata.SourceType, metadata.ID, pageNum)

	// Upload to storage
	storedPath, err := p.storage.UploadBytes(ctx, buf.Bytes(), path, "image/png")
	if err != nil {
		return "", "", fmt.Errorf("failed to upload thumbnail: %w", err)
	}

	// Get URL
	url, err := p.storage.GetURL(ctx, storedPath)
	if err != nil {
		url = "" // Non-fatal
		p.log.WithError(err).Warn("failed to get thumbnail URL")
	}

	return storedPath, url, nil
}

// calculateFileHash calculates SHA256 hash of a file.
func (p *PDFProcessor) calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// GetFullText returns concatenated text from all pages.
func (pd *ProcessedDocument) GetFullText() string {
	var texts []string
	for _, page := range pd.Pages {
		if page.Text != "" {
			texts = append(texts, page.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// GetPageText returns text for a specific page (1-indexed).
func (pd *ProcessedDocument) GetPageText(pageNum int) string {
	if pageNum < 1 || pageNum > len(pd.Pages) {
		return ""
	}
	return pd.Pages[pageNum-1].Text
}

// Helper functions

// cleanText cleans and normalizes extracted text.
func cleanText(text string) string {
	// Remove null characters
	text = strings.ReplaceAll(text, "\x00", "")

	// Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Remove excessive newlines
	reNewlines := regexp.MustCompile(`\n{3,}`)
	text = reNewlines.ReplaceAllString(text, "\n\n")

	// Normalize spaces
	reSpaces := regexp.MustCompile(`[ \t]+`)
	text = reSpaces.ReplaceAllString(text, " ")

	// Trim whitespace from each line
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	text = strings.Join(lines, "\n")

	return strings.TrimSpace(text)
}

// parseKeywords parses a keywords string into a slice.
func parseKeywords(keywords string) []string {
	// Split by common delimiters
	delimiters := regexp.MustCompile(`[,;]`)
	parts := delimiters.Split(keywords, -1)

	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}

	return result
}

// parsePDFDate parses a PDF date string.
func parsePDFDate(dateStr string) time.Time {
	// PDF date format: D:YYYYMMDDHHmmSSOHH'mm'
	// Example: D:20230615120000+05'30'

	dateStr = strings.TrimPrefix(dateStr, "D:")

	formats := []string{
		"20060102150405",
		"20060102150405Z07'00'",
		"20060102150405-07'00'",
		"20060102150405+07'00'",
		"20060102",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}

	// Clean up timezone format
	dateStr = strings.ReplaceAll(dateStr, "'", "")

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}

	return time.Time{}
}

// extractStandardNumber extracts AAOIFI standard number from title.
func extractStandardNumber(title string) string {
	// Match patterns like "FAS 1", "SS 5", "AAOIFI 123"
	patterns := []string{
		`(?i)(FAS|SS|AAOIFI)\s*(\d+)`,
		`(?i)Standard\s*(?:No\.?\s*)?(\d+)`,
		`(?i)Shariah\s*Standard\s*(?:No\.?\s*)?(\d+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(title); len(matches) > 1 {
			return matches[0]
		}
	}

	return ""
}

// ExtractSections extracts document sections based on headers.
func ExtractSections(text string) []Section {
	var sections []Section

	// Match numbered sections (1. Title, 1.1 Subtitle, etc.)
	reSection := regexp.MustCompile(`(?m)^(\d+(?:\.\d+)*)\s*[.:]?\s*(.+)$`)
	matches := reSection.FindAllStringSubmatchIndex(text, -1)

	if len(matches) == 0 {
		// If no numbered sections, treat entire text as one section
		return []Section{{
			Level:   0,
			Title:   "",
			Content: text,
		}}
	}

	for i, match := range matches {
		if len(match) < 6 {
			continue
		}

		number := text[match[2]:match[3]]
		title := text[match[4]:match[5]]

		// Calculate section level from numbering
		level := strings.Count(number, ".") + 1

		// Get content until next section or end
		startIdx := match[1]
		var endIdx int
		if i+1 < len(matches) {
			endIdx = matches[i+1][0]
		} else {
			endIdx = len(text)
		}

		content := strings.TrimSpace(text[startIdx:endIdx])

		sections = append(sections, Section{
			Number:  number,
			Level:   level,
			Title:   strings.TrimSpace(title),
			Content: content,
		})
	}

	return sections
}

// Section represents a document section.
type Section struct {
	Number  string `json:"number"`
	Level   int    `json:"level"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// IsValidPDF checks if a file is a valid PDF.
func IsValidPDF(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	// Read first 5 bytes to check PDF magic number
	header := make([]byte, 5)
	if _, err := file.Read(header); err != nil {
		return false
	}

	return string(header) == "%PDF-"
}

// IsValidPDFBytes checks if byte data is a valid PDF.
func IsValidPDFBytes(data []byte) bool {
	if len(data) < 5 {
		return false
	}
	return string(data[:5]) == "%PDF-"
}

// GetPDFInfo returns basic info about a PDF without full processing.
func GetPDFInfo(path string) (*PDFInfo, error) {
	doc, err := fitz.New(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF: %w", err)
	}
	defer doc.Close()

	metadata := doc.Metadata()

	info := &PDFInfo{
		NumPages: doc.NumPage(),
		Title:    metadata["title"],
		Author:   metadata["author"],
		Subject:  metadata["subject"],
		Keywords: metadata["keywords"],
	}

	// Get file size
	if stat, err := os.Stat(path); err == nil {
		info.FileSize = stat.Size()
	}

	return info, nil
}

// PDFInfo holds basic information about a PDF.
type PDFInfo struct {
	NumPages int    `json:"num_pages"`
	FileSize int64  `json:"file_size"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Subject  string `json:"subject"`
	Keywords string `json:"keywords"`
}
