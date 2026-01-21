// Package agent provides the AI agent orchestrator for ShariaComply AI.
package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/google/uuid"
)

// Message represents a conversation message.
type Message struct {
	ID             uuid.UUID       `json:"id"`
	ConversationID uuid.UUID       `json:"conversation_id"`
	Role           string          `json:"role"` // "user", "assistant", "system"
	Content        string          `json:"content"`
	Citations      []Citation      `json:"citations,omitempty"`
	ToolCalls      []ToolCallInfo  `json:"tool_calls,omitempty"`
	ToolResults    []ToolResultInfo `json:"tool_results,omitempty"`
	TokensUsed     int             `json:"tokens_used,omitempty"`
	ModelUsed      string          `json:"model_used,omitempty"`
	LatencyMs      int             `json:"latency_ms,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// Citation represents a source citation.
type Citation struct {
	Index      int       `json:"index"`
	ChunkID    uuid.UUID `json:"chunk_id"`
	DocumentID uuid.UUID `json:"document_id"`
	Source     string    `json:"source"`
	Title      string    `json:"title"`
	Page       int       `json:"page,omitempty"`
	Section    string    `json:"section,omitempty"`
	Content    string    `json:"content,omitempty"`
	Similarity float64   `json:"similarity,omitempty"`
}

// ToolCallInfo represents a tool call made by the assistant.
type ToolCallInfo struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResultInfo represents the result of a tool call.
type ToolResultInfo struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Conversation represents a chat conversation.
type Conversation struct {
	ID         uuid.UUID         `json:"id"`
	UserID     *uuid.UUID        `json:"user_id,omitempty"`
	Title      string            `json:"title"`
	Summary    string            `json:"summary,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	IsArchived bool              `json:"is_archived"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// MemoryConfig holds configuration for the conversation memory.
type MemoryConfig struct {
	MaxHistoryMessages int           // Maximum messages to keep in context
	MaxTokens          int           // Maximum tokens for context window
	SummaryThreshold   int           // Number of messages before summarization
	CacheTTL           time.Duration // Cache TTL for short-term memory
	EnableCache        bool          // Enable Redis caching
}

// DefaultMemoryConfig returns default memory configuration.
func DefaultMemoryConfig() MemoryConfig {
	return MemoryConfig{
		MaxHistoryMessages: 20,
		MaxTokens:          8000,
		SummaryThreshold:   10,
		CacheTTL:           30 * time.Minute,
		EnableCache:        true,
	}
}

// ConversationMemory manages conversation history and context.
type ConversationMemory struct {
	db     *storage.PostgresDB
	cache  storage.RedisClient
	logger *slog.Logger
	config MemoryConfig
	mu     sync.RWMutex
	// In-memory cache for active conversations
	activeConversations map[uuid.UUID][]Message
}

// NewConversationMemory creates a new conversation memory manager.
func NewConversationMemory(db *storage.PostgresDB, cache storage.RedisClient, logger *slog.Logger, config MemoryConfig) *ConversationMemory {
	if logger == nil {
		logger = slog.Default()
	}

	return &ConversationMemory{
		db:                  db,
		cache:               cache,
		logger:              logger.With("component", "conversation_memory"),
		config:              config,
		activeConversations: make(map[uuid.UUID][]Message),
	}
}

// CreateConversation creates a new conversation.
func (m *ConversationMemory) CreateConversation(ctx context.Context, userID *uuid.UUID, title string) (*Conversation, error) {
	conv := &Conversation{
		ID:         uuid.New(),
		UserID:     userID,
		Title:      title,
		IsArchived: false,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if m.db != nil {
		query := `
			INSERT INTO conversations (id, user_id, title, is_archived, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`
		_, err := m.db.ExecContext(ctx, query,
			conv.ID, conv.UserID, conv.Title, conv.IsArchived, conv.CreatedAt, conv.UpdatedAt,
		)
		if err != nil {
			m.logger.Error("failed to create conversation", "error", err)
			return nil, fmt.Errorf("failed to create conversation: %w", err)
		}
	}

	m.logger.Info("conversation created", "conversation_id", conv.ID, "title", title)
	return conv, nil
}

// GetConversation retrieves a conversation by ID.
func (m *ConversationMemory) GetConversation(ctx context.Context, conversationID uuid.UUID) (*Conversation, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not configured")
	}

	query := `
		SELECT id, user_id, title, summary, metadata, is_archived, created_at, updated_at
		FROM conversations
		WHERE id = $1
	`

	var conv Conversation
	var userID uuid.NullUUID
	var title, summary sql.NullString
	var metadata []byte

	err := m.db.QueryRowContext(ctx, query, conversationID).Scan(
		&conv.ID, &userID, &title, &summary, &metadata, &conv.IsArchived, &conv.CreatedAt, &conv.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	if userID.Valid {
		conv.UserID = &userID.UUID
	}
	conv.Title = title.String
	conv.Summary = summary.String

	if metadata != nil {
		_ = json.Unmarshal(metadata, &conv.Metadata)
	}

	return &conv, nil
}

// GetHistory retrieves conversation history.
func (m *ConversationMemory) GetHistory(ctx context.Context, conversationID uuid.UUID, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = m.config.MaxHistoryMessages
	}

	// Check in-memory cache first
	m.mu.RLock()
	if msgs, ok := m.activeConversations[conversationID]; ok && len(msgs) > 0 {
		m.mu.RUnlock()
		if len(msgs) <= limit {
			return msgs, nil
		}
		return msgs[len(msgs)-limit:], nil
	}
	m.mu.RUnlock()

	// Check Redis cache
	if m.config.EnableCache && m.cache != nil {
		cacheKey := fmt.Sprintf("conv:history:%s", conversationID.String())
		data, err := m.cache.Get(ctx, cacheKey)
		if err == nil && data != "" {
			var msgs []Message
			if err := json.Unmarshal([]byte(data), &msgs); err == nil {
				m.logger.Debug("history cache hit", "conversation_id", conversationID)
				if len(msgs) <= limit {
					return msgs, nil
				}
				return msgs[len(msgs)-limit:], nil
			}
		}
	}

	// Fetch from database
	if m.db == nil {
		return []Message{}, nil
	}

	query := `
		SELECT id, conversation_id, role, content, citations, tool_calls, tool_results,
			   tokens_used, model_used, latency_ms, created_at
		FROM messages
		WHERE conversation_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := m.db.QueryContext(ctx, query, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get history: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var citations, toolCalls, toolResults []byte
		var tokensUsed, latencyMs sql.NullInt32
		var modelUsed sql.NullString

		err := rows.Scan(
			&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content,
			&citations, &toolCalls, &toolResults,
			&tokensUsed, &modelUsed, &latencyMs, &msg.CreatedAt,
		)
		if err != nil {
			m.logger.Error("failed to scan message", "error", err)
			continue
		}

		if citations != nil {
			_ = json.Unmarshal(citations, &msg.Citations)
		}
		if toolCalls != nil {
			_ = json.Unmarshal(toolCalls, &msg.ToolCalls)
		}
		if toolResults != nil {
			_ = json.Unmarshal(toolResults, &msg.ToolResults)
		}

		msg.TokensUsed = int(tokensUsed.Int32)
		msg.ModelUsed = modelUsed.String
		msg.LatencyMs = int(latencyMs.Int32)

		messages = append(messages, msg)
	}

	// Reverse to get chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	// Cache in Redis
	if m.config.EnableCache && m.cache != nil && len(messages) > 0 {
		cacheKey := fmt.Sprintf("conv:history:%s", conversationID.String())
		if data, err := json.Marshal(messages); err == nil {
			_ = m.cache.Set(ctx, cacheKey, data, m.config.CacheTTL)
		}
	}

	// Update in-memory cache
	m.mu.Lock()
	m.activeConversations[conversationID] = messages
	m.mu.Unlock()

	return messages, nil
}

// SaveMessage saves a message to the conversation.
func (m *ConversationMemory) SaveMessage(ctx context.Context, msg Message) error {
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}

	// Update in-memory cache
	m.mu.Lock()
	m.activeConversations[msg.ConversationID] = append(m.activeConversations[msg.ConversationID], msg)
	m.mu.Unlock()

	// Save to database
	if m.db != nil {
		citations, _ := json.Marshal(msg.Citations)
		toolCalls, _ := json.Marshal(msg.ToolCalls)
		toolResults, _ := json.Marshal(msg.ToolResults)

		query := `
			INSERT INTO messages (id, conversation_id, role, content, citations, tool_calls, tool_results,
								  tokens_used, model_used, latency_ms, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`

		var tokensUsed, latencyMs sql.NullInt32
		if msg.TokensUsed > 0 {
			tokensUsed = sql.NullInt32{Int32: int32(msg.TokensUsed), Valid: true}
		}
		if msg.LatencyMs > 0 {
			latencyMs = sql.NullInt32{Int32: int32(msg.LatencyMs), Valid: true}
		}

		var modelUsed sql.NullString
		if msg.ModelUsed != "" {
			modelUsed = sql.NullString{String: msg.ModelUsed, Valid: true}
		}

		_, err := m.db.ExecContext(ctx, query,
			msg.ID, msg.ConversationID, msg.Role, msg.Content,
			citations, toolCalls, toolResults,
			tokensUsed, modelUsed, latencyMs, msg.CreatedAt,
		)
		if err != nil {
			m.logger.Error("failed to save message", "error", err)
			return fmt.Errorf("failed to save message: %w", err)
		}
	}

	// Invalidate cache
	if m.config.EnableCache && m.cache != nil {
		cacheKey := fmt.Sprintf("conv:history:%s", msg.ConversationID.String())
		_ = m.cache.Del(ctx, cacheKey)
	}

	m.logger.Debug("message saved",
		"message_id", msg.ID,
		"conversation_id", msg.ConversationID,
		"role", msg.Role,
	)

	return nil
}

// Save is a convenience method to save a user message and assistant response.
func (m *ConversationMemory) Save(ctx context.Context, conversationID uuid.UUID, userMessage, assistantResponse string) error {
	// Save user message
	userMsg := Message{
		ID:             uuid.New(),
		ConversationID: conversationID,
		Role:           "user",
		Content:        userMessage,
		CreatedAt:      time.Now(),
	}
	if err := m.SaveMessage(ctx, userMsg); err != nil {
		return err
	}

	// Save assistant message
	assistantMsg := Message{
		ID:             uuid.New(),
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        assistantResponse,
		CreatedAt:      time.Now(),
	}
	return m.SaveMessage(ctx, assistantMsg)
}

// GetContextMessages retrieves messages that fit within the token limit.
func (m *ConversationMemory) GetContextMessages(ctx context.Context, conversationID uuid.UUID, maxTokens int) ([]Message, error) {
	if maxTokens <= 0 {
		maxTokens = m.config.MaxTokens
	}

	messages, err := m.GetHistory(ctx, conversationID, m.config.MaxHistoryMessages)
	if err != nil {
		return nil, err
	}

	if len(messages) == 0 {
		return messages, nil
	}

	// Calculate tokens (rough estimate: 1 token ~ 4 chars)
	totalTokens := 0
	var contextMessages []Message

	// Add messages from most recent, until token limit
	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := estimateTokens(messages[i].Content)
		if totalTokens+msgTokens > maxTokens {
			break
		}
		contextMessages = append([]Message{messages[i]}, contextMessages...)
		totalTokens += msgTokens
	}

	return contextMessages, nil
}

// Summarize creates a summary of the conversation history.
func (m *ConversationMemory) Summarize(ctx context.Context, conversationID uuid.UUID) (string, error) {
	messages, err := m.GetHistory(ctx, conversationID, 0)
	if err != nil {
		return "", err
	}

	if len(messages) == 0 {
		return "", nil
	}

	// Build a simple summary of the conversation topics
	var topics []string
	for _, msg := range messages {
		if msg.Role == "user" && len(msg.Content) > 10 {
			// Extract key topics from user messages
			topic := extractTopic(msg.Content)
			if topic != "" && !containsString(topics, topic) {
				topics = append(topics, topic)
			}
		}
	}

	summary := fmt.Sprintf("Conversation with %d messages discussing: %s",
		len(messages),
		strings.Join(topics, ", "),
	)

	// Save summary to database
	if m.db != nil {
		query := `UPDATE conversations SET summary = $1, updated_at = $2 WHERE id = $3`
		_, err = m.db.ExecContext(ctx, query, summary, time.Now(), conversationID)
		if err != nil {
			m.logger.Warn("failed to save summary", "error", err)
		}
	}

	return summary, nil
}

// Clear removes all messages from a conversation.
func (m *ConversationMemory) Clear(ctx context.Context, conversationID uuid.UUID) error {
	// Clear in-memory cache
	m.mu.Lock()
	delete(m.activeConversations, conversationID)
	m.mu.Unlock()

	// Clear Redis cache
	if m.config.EnableCache && m.cache != nil {
		cacheKey := fmt.Sprintf("conv:history:%s", conversationID.String())
		_ = m.cache.Del(ctx, cacheKey)
	}

	// Clear from database
	if m.db != nil {
		query := `DELETE FROM messages WHERE conversation_id = $1`
		_, err := m.db.ExecContext(ctx, query, conversationID)
		if err != nil {
			m.logger.Error("failed to clear conversation", "error", err)
			return fmt.Errorf("failed to clear conversation: %w", err)
		}
	}

	m.logger.Info("conversation cleared", "conversation_id", conversationID)
	return nil
}

// ArchiveConversation archives a conversation.
func (m *ConversationMemory) ArchiveConversation(ctx context.Context, conversationID uuid.UUID) error {
	// Clear from active cache
	m.mu.Lock()
	delete(m.activeConversations, conversationID)
	m.mu.Unlock()

	if m.db != nil {
		query := `UPDATE conversations SET is_archived = true, updated_at = $1 WHERE id = $2`
		_, err := m.db.ExecContext(ctx, query, time.Now(), conversationID)
		if err != nil {
			return fmt.Errorf("failed to archive conversation: %w", err)
		}
	}

	m.logger.Info("conversation archived", "conversation_id", conversationID)
	return nil
}

// ListConversations lists conversations for a user.
func (m *ConversationMemory) ListConversations(ctx context.Context, userID *uuid.UUID, includeArchived bool, limit int) ([]Conversation, error) {
	if m.db == nil {
		return []Conversation{}, nil
	}

	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, user_id, title, summary, is_archived, created_at, updated_at
		FROM conversations
		WHERE ($1::uuid IS NULL OR user_id = $1)
		  AND ($2 OR is_archived = false)
		ORDER BY updated_at DESC
		LIMIT $3
	`

	rows, err := m.db.QueryContext(ctx, query, userID, includeArchived, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	defer rows.Close()

	var conversations []Conversation
	for rows.Next() {
		var conv Conversation
		var userIDNull uuid.NullUUID
		var title, summary sql.NullString

		err := rows.Scan(&conv.ID, &userIDNull, &title, &summary, &conv.IsArchived, &conv.CreatedAt, &conv.UpdatedAt)
		if err != nil {
			continue
		}

		if userIDNull.Valid {
			conv.UserID = &userIDNull.UUID
		}
		conv.Title = title.String
		conv.Summary = summary.String

		conversations = append(conversations, conv)
	}

	return conversations, nil
}

// Helper functions

// estimateTokens provides a rough token count estimate.
func estimateTokens(text string) int {
	// Rough estimate: 1 token ~ 4 characters for English text
	return len(text) / 4
}

// extractTopic extracts a brief topic description from a message.
func extractTopic(content string) string {
	// Take first 50 chars or first sentence
	content = strings.TrimSpace(content)
	if len(content) == 0 {
		return ""
	}

	// Find first sentence
	if idx := strings.Index(content, ". "); idx > 0 && idx < 100 {
		return content[:idx]
	}
	if idx := strings.Index(content, "? "); idx > 0 && idx < 100 {
		return content[:idx]
	}

	if len(content) > 50 {
		// Find word boundary
		truncated := content[:50]
		if lastSpace := strings.LastIndex(truncated, " "); lastSpace > 30 {
			return truncated[:lastSpace] + "..."
		}
		return truncated + "..."
	}

	return content
}

// containsString checks if a slice contains a string.
func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
