// Package storage provides database models and repository interfaces.
package storage

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Document represents a source document (PDF, web page).
type Document struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	SourceType     string          `json:"source_type" db:"source_type"`
	Title          string          `json:"title" db:"title"`
	FileName       sql.NullString  `json:"file_name" db:"file_name"`
	FilePath       sql.NullString  `json:"file_path" db:"file_path"`
	OriginalURL    sql.NullString  `json:"original_url" db:"original_url"`
	Category       sql.NullString  `json:"category" db:"category"`
	StandardNumber sql.NullString  `json:"standard_number" db:"standard_number"`
	EffectiveDate  sql.NullTime    `json:"effective_date" db:"effective_date"`
	ContentHash    sql.NullString  `json:"content_hash" db:"content_hash"`
	TotalPages     sql.NullInt32   `json:"total_pages" db:"total_pages"`
	Language       string          `json:"language" db:"language"`
	Metadata       json.RawMessage `json:"metadata" db:"metadata"`
	IsActive       bool            `json:"is_active" db:"is_active"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// Chunk represents a text chunk with embedding.
type Chunk struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	DocumentID       uuid.UUID       `json:"document_id" db:"document_id"`
	Content          string          `json:"content" db:"content"`
	Embedding        []float32       `json:"embedding,omitempty" db:"embedding"`
	PageNumber       sql.NullInt32   `json:"page_number" db:"page_number"`
	SectionTitle     sql.NullString  `json:"section_title" db:"section_title"`
	SectionHierarchy []string        `json:"section_hierarchy" db:"section_hierarchy"`
	ChunkIndex       int             `json:"chunk_index" db:"chunk_index"`
	StartChar        sql.NullInt32   `json:"start_char" db:"start_char"`
	EndChar          sql.NullInt32   `json:"end_char" db:"end_char"`
	TokenCount       sql.NullInt32   `json:"token_count" db:"token_count"`
	Metadata         json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
}

// ChunkVisual represents visual citation data for a chunk.
type ChunkVisual struct {
	ID                   uuid.UUID       `json:"id" db:"id"`
	ChunkID              uuid.UUID       `json:"chunk_id" db:"chunk_id"`
	PageImagePath        sql.NullString  `json:"page_image_path" db:"page_image_path"`
	ThumbnailPath        sql.NullString  `json:"thumbnail_path" db:"thumbnail_path"`
	HighlightedImagePath sql.NullString  `json:"highlighted_image_path" db:"highlighted_image_path"`
	BoundingBox          json.RawMessage `json:"bounding_box" db:"bounding_box"`
	TextCoordinates      json.RawMessage `json:"text_coordinates" db:"text_coordinates"`
	CreatedAt            time.Time       `json:"created_at" db:"created_at"`
}

// BoundingBox represents the coordinates of a text region.
type BoundingBox struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	PageWidth  float64 `json:"page_width"`
	PageHeight float64 `json:"page_height"`
}

// Conversation represents a chat conversation.
type Conversation struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	UserID     uuid.NullUUID   `json:"user_id" db:"user_id"`
	Title      sql.NullString  `json:"title" db:"title"`
	Summary    sql.NullString  `json:"summary" db:"summary"`
	Metadata   json.RawMessage `json:"metadata" db:"metadata"`
	IsArchived bool            `json:"is_archived" db:"is_archived"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at" db:"updated_at"`
}

// Message represents a chat message.
type Message struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	ConversationID uuid.UUID       `json:"conversation_id" db:"conversation_id"`
	Role           string          `json:"role" db:"role"`
	Content        string          `json:"content" db:"content"`
	Citations      json.RawMessage `json:"citations" db:"citations"`
	ToolCalls      json.RawMessage `json:"tool_calls" db:"tool_calls"`
	ToolResults    json.RawMessage `json:"tool_results" db:"tool_results"`
	TokensUsed     sql.NullInt32   `json:"tokens_used" db:"tokens_used"`
	ModelUsed      sql.NullString  `json:"model_used" db:"model_used"`
	LatencyMs      sql.NullInt32   `json:"latency_ms" db:"latency_ms"`
	Metadata       json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
}

// Citation represents a source citation in a response.
type Citation struct {
	Index      int       `json:"index"`
	ChunkID    uuid.UUID `json:"chunk_id"`
	DocumentID uuid.UUID `json:"document_id"`
	Source     string    `json:"source"`
	Title      string    `json:"title"`
	Page       int       `json:"page,omitempty"`
	Section    string    `json:"section,omitempty"`
	Content    string    `json:"content"`
	Similarity float64   `json:"similarity,omitempty"`
}

// CrawlLog represents a crawl/ingestion log entry.
type CrawlLog struct {
	ID                 uuid.UUID       `json:"id" db:"id"`
	SourceType         string          `json:"source_type" db:"source_type"`
	URL                sql.NullString  `json:"url" db:"url"`
	Status             string          `json:"status" db:"status"`
	DocumentsFound     int             `json:"documents_found" db:"documents_found"`
	DocumentsProcessed int             `json:"documents_processed" db:"documents_processed"`
	ChunksCreated      int             `json:"chunks_created" db:"chunks_created"`
	ErrorMessage       sql.NullString  `json:"error_message" db:"error_message"`
	StartedAt          sql.NullTime    `json:"started_at" db:"started_at"`
	CompletedAt        sql.NullTime    `json:"completed_at" db:"completed_at"`
	Metadata           json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
}

// SearchResult represents a search result from vector similarity search.
type SearchResult struct {
	ChunkID       uuid.UUID `json:"chunk_id"`
	DocumentID    uuid.UUID `json:"document_id"`
	Content       string    `json:"content"`
	Similarity    float64   `json:"similarity"`
	PageNumber    int       `json:"page_number"`
	SectionTitle  string    `json:"section_title"`
	SourceType    string    `json:"source_type"`
	DocumentTitle string    `json:"document_title"`
}
