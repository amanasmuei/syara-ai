package visual

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/pkg/logger"
	"github.com/gen2brain/go-fitz"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// PDFExtractor provides services for extracting pages from PDF documents.
type PDFExtractor struct {
	storage storage.ObjectStorage
	log     *logger.Logger
	// RenderDPI is the DPI for rendering pages as images (default: 300)
	RenderDPI int
}

// NewPDFExtractor creates a new PDFExtractor instance.
func NewPDFExtractor(store storage.ObjectStorage, log *logger.Logger) *PDFExtractor {
	if log == nil {
		log = logger.Default()
	}
	return &PDFExtractor{
		storage:   store,
		log:       log.WithComponent("pdf-extractor"),
		RenderDPI: 300,
	}
}

// ExtractPageResult contains the result of a page extraction.
type ExtractPageResult struct {
	// PDFPath is the storage path of the extracted page PDF
	PDFPath string `json:"pdf_path,omitempty"`
	// PDFURL is the public URL of the extracted page PDF
	PDFURL string `json:"pdf_url,omitempty"`
	// ImagePath is the storage path of the page rendered as image
	ImagePath string `json:"image_path,omitempty"`
	// ImageURL is the public URL of the page image
	ImageURL string `json:"image_url,omitempty"`
	// PageNumber is the extracted page number (1-indexed)
	PageNumber int `json:"page_number"`
	// Width is the page width in pixels (for image)
	Width int `json:"width,omitempty"`
	// Height is the page height in pixels (for image)
	Height int `json:"height,omitempty"`
	// TotalPages is the total number of pages in the source document
	TotalPages int `json:"total_pages"`
}

// ExtractPageOptions configures page extraction behavior.
type ExtractPageOptions struct {
	// ExtractPDF indicates whether to extract the page as a PDF
	ExtractPDF bool
	// ExtractImage indicates whether to render the page as an image
	ExtractImage bool
	// ImageDPI is the DPI for rendering (default: 300)
	ImageDPI int
}

// DefaultExtractPageOptions returns sensible defaults.
func DefaultExtractPageOptions() ExtractPageOptions {
	return ExtractPageOptions{
		ExtractPDF:   true,
		ExtractImage: true,
		ImageDPI:     300,
	}
}

// ExtractPage extracts a single page from a PDF document.
func (e *PDFExtractor) ExtractPage(
	ctx context.Context,
	pdfPath string,
	pageNum int,
	opts ExtractPageOptions,
	documentID string,
) (*ExtractPageResult, error) {
	e.log.Info("extracting page from PDF",
		"pdf_path", pdfPath,
		"page_num", pageNum,
	)

	// Download the PDF
	pdfBytes, err := e.storage.Download(ctx, pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	// Get total page count for validation
	totalPages, err := e.getPageCount(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to get page count: %w", err)
	}

	// Validate page number
	if pageNum < 1 || pageNum > totalPages {
		return nil, fmt.Errorf("invalid page number %d, document has %d pages", pageNum, totalPages)
	}

	result := &ExtractPageResult{
		PageNumber: pageNum,
		TotalPages: totalPages,
	}

	// Extract as PDF
	if opts.ExtractPDF {
		pdfResult, err := e.extractPageAsPDF(ctx, pdfBytes, pageNum, documentID)
		if err != nil {
			e.log.WithError(err).Warn("failed to extract page as PDF")
		} else {
			result.PDFPath = pdfResult.path
			result.PDFURL = pdfResult.url
		}
	}

	// Extract as image
	if opts.ExtractImage {
		imgResult, err := e.extractPageAsImage(ctx, pdfBytes, pageNum, opts.ImageDPI, documentID)
		if err != nil {
			e.log.WithError(err).Warn("failed to extract page as image")
		} else {
			result.ImagePath = imgResult.path
			result.ImageURL = imgResult.url
			result.Width = imgResult.width
			result.Height = imgResult.height
		}
	}

	e.log.Info("page extracted successfully",
		"page_num", pageNum,
		"total_pages", totalPages,
		"pdf_extracted", result.PDFPath != "",
		"image_extracted", result.ImagePath != "",
	)

	return result, nil
}

// ExtractPageRange extracts a range of pages from a PDF document.
func (e *PDFExtractor) ExtractPageRange(
	ctx context.Context,
	pdfPath string,
	startPage, endPage int,
	documentID string,
) (*ExtractPageResult, error) {
	e.log.Info("extracting page range from PDF",
		"pdf_path", pdfPath,
		"start_page", startPage,
		"end_page", endPage,
	)

	// Download the PDF
	pdfBytes, err := e.storage.Download(ctx, pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	// Get total page count for validation
	totalPages, err := e.getPageCount(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to get page count: %w", err)
	}

	// Validate page numbers
	if startPage < 1 {
		startPage = 1
	}
	if endPage > totalPages {
		endPage = totalPages
	}
	if startPage > endPage {
		return nil, fmt.Errorf("invalid page range: start=%d, end=%d", startPage, endPage)
	}

	// Create temporary file for processing
	tmpFile, err := os.CreateTemp("", "pdf-extract-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Create output file
	outFile, err := os.CreateTemp("", "pdf-range-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	// Extract page range using pdfcpu
	pageSelection := []string{fmt.Sprintf("%d-%d", startPage, endPage)}
	conf := model.NewDefaultConfiguration()
	if err := api.ExtractPagesFile(tmpPath, outPath, pageSelection, conf); err != nil {
		return nil, fmt.Errorf("failed to extract pages: %w", err)
	}

	// Read the extracted PDF
	extractedBytes, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read extracted PDF: %w", err)
	}

	// Upload to storage
	storagePath := fmt.Sprintf("%s/%s/pages-%d-%d.pdf", storage.PathGenerated, documentID, startPage, endPage)
	uploadedPath, err := e.storage.UploadBytes(ctx, extractedBytes, storagePath, "application/pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to upload extracted PDF: %w", err)
	}

	url, err := e.storage.GetURL(ctx, uploadedPath)
	if err != nil {
		e.log.WithError(err).Warn("failed to get extracted PDF URL")
		url = ""
	}

	e.log.Info("page range extracted successfully",
		"start_page", startPage,
		"end_page", endPage,
		"output_path", uploadedPath,
	)

	return &ExtractPageResult{
		PDFPath:    uploadedPath,
		PDFURL:     url,
		PageNumber: startPage,
		TotalPages: totalPages,
	}, nil
}

// RenderPageAsImage renders a specific page as an image.
func (e *PDFExtractor) RenderPageAsImage(
	ctx context.Context,
	pdfPath string,
	pageNum int,
	dpi int,
	documentID string,
) (*ExtractPageResult, error) {
	e.log.Info("rendering page as image",
		"pdf_path", pdfPath,
		"page_num", pageNum,
		"dpi", dpi,
	)

	// Download the PDF
	pdfBytes, err := e.storage.Download(ctx, pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	// Get total page count for validation
	totalPages, err := e.getPageCount(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to get page count: %w", err)
	}

	// Validate page number
	if pageNum < 1 || pageNum > totalPages {
		return nil, fmt.Errorf("invalid page number %d, document has %d pages", pageNum, totalPages)
	}

	// Render the page
	imgResult, err := e.extractPageAsImage(ctx, pdfBytes, pageNum, dpi, documentID)
	if err != nil {
		return nil, fmt.Errorf("failed to render page: %w", err)
	}

	e.log.Info("page rendered successfully",
		"page_num", pageNum,
		"output_path", imgResult.path,
		"dimensions", fmt.Sprintf("%dx%d", imgResult.width, imgResult.height),
	)

	return &ExtractPageResult{
		ImagePath:  imgResult.path,
		ImageURL:   imgResult.url,
		PageNumber: pageNum,
		Width:      imgResult.width,
		Height:     imgResult.height,
		TotalPages: totalPages,
	}, nil
}

// GetPageCount returns the total number of pages in a PDF.
func (e *PDFExtractor) GetPageCount(ctx context.Context, pdfPath string) (int, error) {
	// Download the PDF
	pdfBytes, err := e.storage.Download(ctx, pdfPath)
	if err != nil {
		return 0, fmt.Errorf("failed to download PDF: %w", err)
	}

	return e.getPageCount(pdfBytes)
}

// getPageCount returns the page count from PDF bytes using go-fitz.
func (e *PDFExtractor) getPageCount(pdfBytes []byte) (int, error) {
	// Create a temporary file (go-fitz requires file path)
	tmpFile, err := os.CreateTemp("", "pdf-count-*.pdf")
	if err != nil {
		return 0, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return 0, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Open with go-fitz
	doc, err := fitz.New(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open PDF: %w", err)
	}
	defer doc.Close()

	return doc.NumPage(), nil
}

// extractResult holds internal extraction results.
type extractResult struct {
	path   string
	url    string
	width  int
	height int
}

// extractPageAsPDF extracts a single page as a PDF.
func (e *PDFExtractor) extractPageAsPDF(
	ctx context.Context,
	pdfBytes []byte,
	pageNum int,
	documentID string,
) (*extractResult, error) {
	// Create temporary file for processing
	tmpFile, err := os.CreateTemp("", "pdf-extract-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Create output file
	outFile, err := os.CreateTemp("", "pdf-page-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	// Extract single page using pdfcpu
	pageSelection := []string{fmt.Sprintf("%d", pageNum)}
	conf := model.NewDefaultConfiguration()
	if err := api.ExtractPagesFile(tmpPath, outPath, pageSelection, conf); err != nil {
		return nil, fmt.Errorf("failed to extract page: %w", err)
	}

	// Read the extracted PDF
	extractedBytes, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read extracted PDF: %w", err)
	}

	// Upload to storage
	storagePath := fmt.Sprintf("%s/%s/page-%d.pdf", storage.PathGenerated, documentID, pageNum)
	uploadedPath, err := e.storage.UploadBytes(ctx, extractedBytes, storagePath, "application/pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to upload extracted PDF: %w", err)
	}

	url, err := e.storage.GetURL(ctx, uploadedPath)
	if err != nil {
		e.log.WithError(err).Warn("failed to get extracted PDF URL")
		url = ""
	}

	return &extractResult{
		path: uploadedPath,
		url:  url,
	}, nil
}

// extractPageAsImage renders a page as an image using go-fitz.
func (e *PDFExtractor) extractPageAsImage(
	ctx context.Context,
	pdfBytes []byte,
	pageNum int,
	dpi int,
	documentID string,
) (*extractResult, error) {
	if dpi <= 0 {
		dpi = e.RenderDPI
	}

	// Create a temporary file (go-fitz requires file path)
	tmpFile, err := os.CreateTemp("", "pdf-render-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Open with go-fitz
	doc, err := fitz.New(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF: %w", err)
	}
	defer doc.Close()

	// Render the page (pageNum is 1-indexed, go-fitz uses 0-indexed)
	img, err := doc.Image(pageNum - 1)
	if err != nil {
		return nil, fmt.Errorf("failed to render page: %w", err)
	}

	// Encode as PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}

	// Upload to storage
	storagePath := storage.BuildPagePath("generated", documentID, pageNum)
	uploadedPath, err := e.storage.UploadBytes(ctx, buf.Bytes(), storagePath, "image/png")
	if err != nil {
		return nil, fmt.Errorf("failed to upload page image: %w", err)
	}

	url, err := e.storage.GetURL(ctx, uploadedPath)
	if err != nil {
		e.log.WithError(err).Warn("failed to get page image URL")
		url = ""
	}

	bounds := img.Bounds()
	return &extractResult{
		path:   uploadedPath,
		url:    url,
		width:  bounds.Dx(),
		height: bounds.Dy(),
	}, nil
}

// ExtractPageBytes extracts a single page from PDF bytes and returns it as bytes.
// Useful for in-memory processing without storage.
func (e *PDFExtractor) ExtractPageBytes(pdfBytes []byte, pageNum int) ([]byte, error) {
	// Create temporary file for processing
	tmpFile, err := os.CreateTemp("", "pdf-extract-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Get page count for validation
	totalPages, err := e.getPageCount(pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to get page count: %w", err)
	}

	if pageNum < 1 || pageNum > totalPages {
		return nil, fmt.Errorf("invalid page number %d, document has %d pages", pageNum, totalPages)
	}

	// Create output file
	outFile, err := os.CreateTemp("", "pdf-page-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	// Extract single page using pdfcpu
	pageSelection := []string{fmt.Sprintf("%d", pageNum)}
	conf := model.NewDefaultConfiguration()
	if err := api.ExtractPagesFile(tmpPath, outPath, pageSelection, conf); err != nil {
		return nil, fmt.Errorf("failed to extract page: %w", err)
	}

	// Read the extracted PDF
	return os.ReadFile(outPath)
}

// RenderPageBytes renders a specific page as a PNG image and returns bytes.
func (e *PDFExtractor) RenderPageBytes(pdfBytes []byte, pageNum int, dpi int) ([]byte, image.Image, error) {
	if dpi <= 0 {
		dpi = e.RenderDPI
	}

	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "pdf-render-*.pdf")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return nil, nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Open with go-fitz
	doc, err := fitz.New(tmpPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open PDF: %w", err)
	}
	defer doc.Close()

	// Validate page number
	totalPages := doc.NumPage()
	if pageNum < 1 || pageNum > totalPages {
		return nil, nil, fmt.Errorf("invalid page number %d, document has %d pages", pageNum, totalPages)
	}

	// Render the page (0-indexed)
	img, err := doc.Image(pageNum - 1)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to render page: %w", err)
	}

	// Encode as PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, nil, fmt.Errorf("failed to encode image: %w", err)
	}

	return buf.Bytes(), img, nil
}

// GetPDFMetadata returns metadata about a PDF document.
func (e *PDFExtractor) GetPDFMetadata(ctx context.Context, pdfPath string) (*PDFMetadata, error) {
	// Download the PDF
	pdfBytes, err := e.storage.Download(ctx, pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to download PDF: %w", err)
	}

	return e.GetPDFMetadataFromBytes(pdfBytes)
}

// PDFMetadata contains metadata about a PDF document.
type PDFMetadata struct {
	Title        string `json:"title,omitempty"`
	Author       string `json:"author,omitempty"`
	Subject      string `json:"subject,omitempty"`
	Keywords     string `json:"keywords,omitempty"`
	Creator      string `json:"creator,omitempty"`
	Producer     string `json:"producer,omitempty"`
	CreationDate string `json:"creation_date,omitempty"`
	ModDate      string `json:"mod_date,omitempty"`
	TotalPages   int    `json:"total_pages"`
	FileSizeKB   int64  `json:"file_size_kb"`
}

// GetPDFMetadataFromBytes extracts metadata from PDF bytes.
func (e *PDFExtractor) GetPDFMetadataFromBytes(pdfBytes []byte) (*PDFMetadata, error) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "pdf-meta-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Open with go-fitz
	doc, err := fitz.New(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF: %w", err)
	}
	defer doc.Close()

	// Get metadata
	meta := doc.Metadata()

	return &PDFMetadata{
		Title:        meta["title"],
		Author:       meta["author"],
		Subject:      meta["subject"],
		Keywords:     meta["keywords"],
		Creator:      meta["creator"],
		Producer:     meta["producer"],
		CreationDate: meta["creationDate"],
		ModDate:      meta["modDate"],
		TotalPages:   doc.NumPage(),
		FileSizeKB:   int64(len(pdfBytes)) / 1024,
	}, nil
}

// ExtractText extracts text from a specific page.
func (e *PDFExtractor) ExtractText(ctx context.Context, pdfPath string, pageNum int) (string, error) {
	// Download the PDF
	pdfBytes, err := e.storage.Download(ctx, pdfPath)
	if err != nil {
		return "", fmt.Errorf("failed to download PDF: %w", err)
	}

	return e.ExtractTextFromBytes(pdfBytes, pageNum)
}

// ExtractTextFromBytes extracts text from a specific page of PDF bytes.
func (e *PDFExtractor) ExtractTextFromBytes(pdfBytes []byte, pageNum int) (string, error) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "pdf-text-*.pdf")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(pdfBytes); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Open with go-fitz
	doc, err := fitz.New(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer doc.Close()

	// Validate page number
	totalPages := doc.NumPage()
	if pageNum < 1 || pageNum > totalPages {
		return "", fmt.Errorf("invalid page number %d, document has %d pages", pageNum, totalPages)
	}

	// Extract text (0-indexed)
	text, err := doc.Text(pageNum - 1)
	if err != nil {
		return "", fmt.Errorf("failed to extract text: %w", err)
	}

	return text, nil
}

// BuildPagePDFPath constructs a storage path for extracted page PDFs.
func BuildPagePDFPath(documentID string, pageNum int) string {
	return filepath.Join(storage.PathGenerated, documentID, fmt.Sprintf("page-%d.pdf", pageNum))
}

// BuildPageRangePDFPath constructs a storage path for extracted page range PDFs.
func BuildPageRangePDFPath(documentID string, startPage, endPage int) string {
	return filepath.Join(storage.PathGenerated, documentID, fmt.Sprintf("pages-%d-%d.pdf", startPage, endPage))
}
