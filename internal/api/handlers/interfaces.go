// Package handlers provides HTTP request handlers for the API.
package handlers

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Database defines the interface for database operations needed by handlers.
type Database interface {
	// Health checks database connectivity.
	Health(ctx context.Context) error

	// Conversation operations
	CreateConversation(ctx context.Context, conv *Conversation) error
	GetConversation(ctx context.Context, id uuid.UUID) (*Conversation, error)
	UpdateConversation(ctx context.Context, conv *Conversation) error
	DeleteConversation(ctx context.Context, id uuid.UUID) error
	ListConversations(ctx context.Context, opts ListOptions) ([]*Conversation, int, error)

	// Message operations
	CreateMessage(ctx context.Context, msg *Message) error
	GetMessages(ctx context.Context, conversationID uuid.UUID, opts ListOptions) ([]*Message, int, error)

	// Document operations
	GetDocument(ctx context.Context, id uuid.UUID) (*Document, error)
	GetChunkVisual(ctx context.Context, chunkID uuid.UUID) (*ChunkVisual, error)
}

// ObjectStorage defines the interface for object storage operations.
type ObjectStorage interface {
	// Health checks storage connectivity.
	Health(ctx context.Context) error

	// GenerateSignedURL generates a presigned URL for downloading.
	GenerateSignedURL(ctx context.Context, path string, expiry time.Duration) (string, error)

	// Exists checks if an object exists.
	Exists(ctx context.Context, path string) (bool, error)
}

// ChatService defines the interface for chat processing.
type ChatService interface {
	// Process processes a chat message and returns a response.
	Process(ctx context.Context, req ChatRequest) (*ChatResult, error)
}

// ListOptions holds pagination options.
type ListOptions struct {
	Limit  int
	Offset int
}

// Conversation represents a chat conversation.
type Conversation struct {
	ID           uuid.UUID  `json:"id"`
	UserID       *uuid.UUID `json:"user_id,omitempty"`
	Title        string     `json:"title"`
	Summary      string     `json:"summary,omitempty"`
	IsArchived   bool       `json:"is_archived"`
	MessageCount int        `json:"message_count"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Message represents a chat message.
type Message struct {
	ID             uuid.UUID   `json:"id"`
	ConversationID uuid.UUID   `json:"conversation_id"`
	Role           string      `json:"role"`
	Content        string      `json:"content"`
	Citations      []Citation  `json:"citations,omitempty"`
	TokensUsed     int         `json:"tokens_used,omitempty"`
	ModelUsed      string      `json:"model_used,omitempty"`
	LatencyMs      int         `json:"latency_ms,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
}

// Citation represents a source citation in a response.
type Citation struct {
	Index        int       `json:"index"`
	ChunkID      uuid.UUID `json:"chunk_id"`
	DocumentID   uuid.UUID `json:"document_id"`
	Source       string    `json:"source"`
	Title        string    `json:"title"`
	Page         int       `json:"page,omitempty"`
	Section      string    `json:"section,omitempty"`
	Content      string    `json:"content"`
	Similarity   float64   `json:"similarity,omitempty"`
	ThumbnailURL string    `json:"thumbnail_url,omitempty"`
	DownloadURL  string    `json:"download_url,omitempty"`
}

// Document represents a source document.
type Document struct {
	ID             uuid.UUID  `json:"id"`
	SourceType     string     `json:"source_type"`
	Title          string     `json:"title"`
	FileName       string     `json:"file_name,omitempty"`
	FilePath       string     `json:"file_path,omitempty"`
	OriginalURL    string     `json:"original_url,omitempty"`
	Category       string     `json:"category,omitempty"`
	StandardNumber string     `json:"standard_number,omitempty"`
	EffectiveDate  *time.Time `json:"effective_date,omitempty"`
	TotalPages     int        `json:"total_pages,omitempty"`
	Language       string     `json:"language"`
	IsActive       bool       `json:"is_active"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ChunkVisual represents visual citation data for a chunk.
type ChunkVisual struct {
	ID                   uuid.UUID `json:"id"`
	ChunkID              uuid.UUID `json:"chunk_id"`
	PageImagePath        string    `json:"page_image_path,omitempty"`
	ThumbnailPath        string    `json:"thumbnail_path,omitempty"`
	HighlightedImagePath string    `json:"highlighted_image_path,omitempty"`
}

// ChatRequest represents a chat request.
type ChatRequest struct {
	ConversationID string `json:"conversation_id,omitempty"`
	Message        string `json:"message"`
}

// ChatResult represents the result of chat processing.
type ChatResult struct {
	ConversationID string     `json:"conversation_id"`
	Answer         string     `json:"answer"`
	Citations      []Citation `json:"citations"`
	Confidence     float64    `json:"confidence"`
	TokensUsed     int        `json:"tokens_used"`
	ModelUsed      string     `json:"model_used"`
}
