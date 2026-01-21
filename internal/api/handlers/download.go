// Package handlers provides HTTP request handlers for the API.
package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// DocumentResponse represents a document with download URLs.
type DocumentResponse struct {
	*Document
	DownloadURL string `json:"download_url,omitempty"`
}

// DownloadType represents the type of download.
type DownloadType string

const (
	// DownloadTypeOriginal downloads the original document.
	DownloadTypeOriginal DownloadType = "original"
	// DownloadTypePage downloads a specific page image.
	DownloadTypePage DownloadType = "page"
	// DownloadTypeHighlighted downloads a highlighted image.
	DownloadTypeHighlighted DownloadType = "highlighted"
	// DownloadTypeThumbnail downloads a thumbnail image.
	DownloadTypeThumbnail DownloadType = "thumbnail"
)

// SignedURLExpiry is the default expiration time for signed URLs.
const SignedURLExpiry = 1 * time.Hour

// GetDocument returns a handler for getting document information.
// GET /api/v1/documents/{id}
//
// Response: Document metadata
func GetDocument(db Database, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse document ID
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			logger.Warn("invalid document ID", "id", idStr, "error", err)
			RespondBadRequest(w, "Invalid document ID")
			return
		}

		// Check if database is available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}

		// Fetch document
		doc, err := db.GetDocument(ctx, id)
		if err != nil {
			logger.Warn("document not found", "id", id, "error", err)
			RespondNotFound(w, "Document not found")
			return
		}

		RespondJSON(w, http.StatusOK, doc)
	}
}

// HandleDownload returns a handler for downloading documents.
// GET /api/v1/documents/{id}/download
//
// Query parameters:
//   - type: download type (original, page, highlighted, thumbnail) - default: original
//   - page: page number for page/thumbnail downloads
//   - chunk_id: chunk ID for highlighted downloads
//
// Response: Redirects to signed URL
func HandleDownload(db Database, storage ObjectStorage, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse document ID
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			logger.Warn("invalid document ID", "id", idStr, "error", err)
			RespondBadRequest(w, "Invalid document ID")
			return
		}

		// Get download type
		downloadType := DownloadType(r.URL.Query().Get("type"))
		if downloadType == "" {
			downloadType = DownloadTypeOriginal
		}

		// Validate download type
		validTypes := map[DownloadType]bool{
			DownloadTypeOriginal:    true,
			DownloadTypePage:        true,
			DownloadTypeHighlighted: true,
			DownloadTypeThumbnail:   true,
		}
		if !validTypes[downloadType] {
			logger.Warn("invalid download type", "type", downloadType)
			RespondBadRequest(w, "Invalid download type. Valid types: original, page, highlighted, thumbnail")
			return
		}

		// Check if services are available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}
		if storage == nil {
			logger.Warn("storage not available")
			RespondServiceUnavailable(w, "Storage service not available")
			return
		}

		// Fetch document information
		doc, err := db.GetDocument(ctx, id)
		if err != nil {
			logger.Warn("document not found", "id", id, "error", err)
			RespondNotFound(w, "Document not found")
			return
		}

		// Determine the file path based on download type
		var filePath string
		switch downloadType {
		case DownloadTypeOriginal:
			if doc.FilePath == "" {
				RespondNotFound(w, "Original file not available")
				return
			}
			filePath = doc.FilePath

		case DownloadTypePage:
			pageStr := r.URL.Query().Get("page")
			if pageStr == "" {
				RespondBadRequest(w, "Page number is required for page download")
				return
			}
			var pageNum int
			if _, err := parsePageNumber(pageStr, &pageNum); err != nil {
				RespondBadRequest(w, "Invalid page number")
				return
			}
			if doc.TotalPages > 0 && pageNum > doc.TotalPages {
				RespondBadRequest(w, "Page number exceeds document pages")
				return
			}
			filePath = buildPagePath(doc.SourceType, id.String(), pageNum)

		case DownloadTypeThumbnail:
			pageStr := r.URL.Query().Get("page")
			if pageStr == "" {
				pageStr = "1" // Default to first page
			}
			var pageNum int
			if _, err := parsePageNumber(pageStr, &pageNum); err != nil {
				RespondBadRequest(w, "Invalid page number")
				return
			}
			filePath = buildThumbnailPath(doc.SourceType, id.String(), pageNum)

		case DownloadTypeHighlighted:
			chunkIDStr := r.URL.Query().Get("chunk_id")
			if chunkIDStr == "" {
				RespondBadRequest(w, "Chunk ID is required for highlighted download")
				return
			}
			chunkID, err := uuid.Parse(chunkIDStr)
			if err != nil {
				RespondBadRequest(w, "Invalid chunk ID")
				return
			}
			// Get chunk visual info
			chunkVisual, err := db.GetChunkVisual(ctx, chunkID)
			if err != nil {
				RespondNotFound(w, "Highlighted image not found")
				return
			}
			if chunkVisual.HighlightedImagePath == "" {
				RespondNotFound(w, "Highlighted image not available")
				return
			}
			filePath = chunkVisual.HighlightedImagePath
		}

		// Check if file exists
		exists, err := storage.Exists(ctx, filePath)
		if err != nil {
			logger.Error("failed to check file existence", "path", filePath, "error", err)
			RespondInternalError(w, "Failed to verify file")
			return
		}
		if !exists {
			logger.Warn("file not found in storage", "path", filePath)
			RespondNotFound(w, "File not found")
			return
		}

		// Generate signed URL
		signedURL, err := storage.GenerateSignedURL(ctx, filePath, SignedURLExpiry)
		if err != nil {
			logger.Error("failed to generate signed URL", "path", filePath, "error", err)
			RespondInternalError(w, "Failed to generate download URL")
			return
		}

		logger.Info("download initiated",
			"document_id", id,
			"type", downloadType,
			"path", filePath,
		)

		// Redirect to signed URL
		http.Redirect(w, r, signedURL, http.StatusTemporaryRedirect)
	}
}

// parsePageNumber parses a page number string.
func parsePageNumber(s string, pageNum *int) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errInvalidPageNumber
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 {
		return 0, errInvalidPageNumber
	}
	*pageNum = n
	return n, nil
}

// errInvalidPageNumber is returned when a page number is invalid.
var errInvalidPageNumber = errors.New("invalid page number")

// buildPagePath constructs a path for page images.
func buildPagePath(sourceType, docID string, pageNum int) string {
	return "pages/" + sourceType + "/" + docID + "/page-" + itoa(pageNum) + ".png"
}

// buildThumbnailPath constructs a path for thumbnails.
func buildThumbnailPath(sourceType, docID string, pageNum int) string {
	return "thumbs/" + sourceType + "/" + docID + "/page-" + itoa(pageNum) + "-thumb.png"
}

// itoa converts an integer to a string (simple implementation).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
