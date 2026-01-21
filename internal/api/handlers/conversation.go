// Package handlers provides HTTP request handlers for the API.
package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ConversationListResponse represents the response for listing conversations.
type ConversationListResponse struct {
	Conversations []*Conversation `json:"conversations"`
	Pagination    Pagination      `json:"pagination"`
}

// ConversationWithMessages represents a conversation with its messages.
type ConversationWithMessages struct {
	*Conversation
	Messages []*Message `json:"messages"`
}

// CreateConversationRequest represents the request to create a conversation.
type CreateConversationRequest struct {
	Title string `json:"title"`
}

// UpdateConversationRequest represents the request to update a conversation.
type UpdateConversationRequest struct {
	Title      *string `json:"title,omitempty"`
	Summary    *string `json:"summary,omitempty"`
	IsArchived *bool   `json:"is_archived,omitempty"`
}

// ListConversations returns a handler for listing conversations.
// GET /api/v1/conversations
//
// Query parameters:
//   - limit: number of results (default: 50, max: 100)
//   - offset: offset for pagination (default: 0)
//
// Response:
//
//	{
//	  "conversations": [...],
//	  "pagination": {"total": 100, "limit": 50, "offset": 0, "has_more": true}
//	}
func ListConversations(db Database, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse pagination parameters
		limit := parseIntQueryParam(r, "limit", 50)
		if limit > 100 {
			limit = 100
		}
		if limit < 1 {
			limit = 1
		}

		offset := parseIntQueryParam(r, "offset", 0)
		if offset < 0 {
			offset = 0
		}

		logger.Info("listing conversations", "limit", limit, "offset", offset)

		// Check if database is available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}

		// Fetch conversations
		conversations, total, err := db.ListConversations(ctx, ListOptions{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			logger.Error("failed to list conversations", "error", err)
			RespondInternalError(w, "Failed to retrieve conversations")
			return
		}

		// Ensure conversations is not nil
		if conversations == nil {
			conversations = []*Conversation{}
		}

		RespondJSON(w, http.StatusOK, ConversationListResponse{
			Conversations: conversations,
			Pagination: Pagination{
				Total:   total,
				Limit:   limit,
				Offset:  offset,
				HasMore: offset+len(conversations) < total,
			},
		})
	}
}

// CreateConversation returns a handler for creating a new conversation.
// POST /api/v1/conversations
//
// Request body:
//
//	{
//	  "title": "New Conversation"
//	}
//
// Response: Created conversation object
func CreateConversation(db Database, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse request body
		var req CreateConversationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn("failed to decode create conversation request", "error", err)
			RespondBadRequest(w, "Invalid request body")
			return
		}

		// Set default title if not provided
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = "New Conversation"
		}

		// Check if database is available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}

		// Create conversation
		now := time.Now()
		conv := &Conversation{
			ID:        uuid.New(),
			Title:     title,
			CreatedAt: now,
			UpdatedAt: now,
		}

		if err := db.CreateConversation(ctx, conv); err != nil {
			logger.Error("failed to create conversation", "error", err)
			RespondInternalError(w, "Failed to create conversation")
			return
		}

		logger.Info("conversation created", "id", conv.ID)
		RespondCreated(w, conv)
	}
}

// GetConversation returns a handler for getting a specific conversation.
// GET /api/v1/conversations/{id}
//
// Response: Conversation object with messages
func GetConversation(db Database, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse conversation ID
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			logger.Warn("invalid conversation ID", "id", idStr, "error", err)
			RespondBadRequest(w, "Invalid conversation ID")
			return
		}

		// Check if database is available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}

		// Fetch conversation
		conv, err := db.GetConversation(ctx, id)
		if err != nil {
			logger.Warn("conversation not found", "id", id, "error", err)
			RespondNotFound(w, "Conversation not found")
			return
		}

		// Fetch messages for the conversation
		messages, _, err := db.GetMessages(ctx, id, ListOptions{
			Limit:  100,
			Offset: 0,
		})
		if err != nil {
			logger.Error("failed to fetch messages", "conversation_id", id, "error", err)
			// Return conversation without messages on error
			RespondJSON(w, http.StatusOK, ConversationWithMessages{
				Conversation: conv,
				Messages:     []*Message{},
			})
			return
		}

		if messages == nil {
			messages = []*Message{}
		}

		RespondJSON(w, http.StatusOK, ConversationWithMessages{
			Conversation: conv,
			Messages:     messages,
		})
	}
}

// UpdateConversation returns a handler for updating a conversation.
// PUT /api/v1/conversations/{id}
//
// Request body:
//
//	{
//	  "title": "Updated Title",
//	  "is_archived": true
//	}
//
// Response: Updated conversation object
func UpdateConversation(db Database, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse conversation ID
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			logger.Warn("invalid conversation ID", "id", idStr, "error", err)
			RespondBadRequest(w, "Invalid conversation ID")
			return
		}

		// Parse request body
		var req UpdateConversationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn("failed to decode update conversation request", "error", err)
			RespondBadRequest(w, "Invalid request body")
			return
		}

		// Check if database is available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}

		// Fetch existing conversation
		conv, err := db.GetConversation(ctx, id)
		if err != nil {
			logger.Warn("conversation not found", "id", id, "error", err)
			RespondNotFound(w, "Conversation not found")
			return
		}

		// Update fields
		if req.Title != nil {
			conv.Title = strings.TrimSpace(*req.Title)
		}
		if req.Summary != nil {
			conv.Summary = strings.TrimSpace(*req.Summary)
		}
		if req.IsArchived != nil {
			conv.IsArchived = *req.IsArchived
		}
		conv.UpdatedAt = time.Now()

		// Save updates
		if err := db.UpdateConversation(ctx, conv); err != nil {
			logger.Error("failed to update conversation", "id", id, "error", err)
			RespondInternalError(w, "Failed to update conversation")
			return
		}

		logger.Info("conversation updated", "id", id)
		RespondJSON(w, http.StatusOK, conv)
	}
}

// DeleteConversation returns a handler for deleting a conversation.
// DELETE /api/v1/conversations/{id}
//
// Response: 204 No Content
func DeleteConversation(db Database, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse conversation ID
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			logger.Warn("invalid conversation ID", "id", idStr, "error", err)
			RespondBadRequest(w, "Invalid conversation ID")
			return
		}

		// Check if database is available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}

		// Check if conversation exists
		_, err = db.GetConversation(ctx, id)
		if err != nil {
			logger.Warn("conversation not found", "id", id, "error", err)
			RespondNotFound(w, "Conversation not found")
			return
		}

		// Delete conversation
		if err := db.DeleteConversation(ctx, id); err != nil {
			logger.Error("failed to delete conversation", "id", id, "error", err)
			RespondInternalError(w, "Failed to delete conversation")
			return
		}

		logger.Info("conversation deleted", "id", id)
		RespondNoContent(w)
	}
}

// GetConversationMessages returns a handler for getting messages in a conversation.
// GET /api/v1/conversations/{id}/messages
//
// Query parameters:
//   - limit: number of results (default: 50, max: 100)
//   - offset: offset for pagination (default: 0)
//
// Response: List of messages with pagination
func GetConversationMessages(db Database, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Parse conversation ID
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			logger.Warn("invalid conversation ID", "id", idStr, "error", err)
			RespondBadRequest(w, "Invalid conversation ID")
			return
		}

		// Parse pagination parameters
		limit := parseIntQueryParam(r, "limit", 50)
		if limit > 100 {
			limit = 100
		}
		if limit < 1 {
			limit = 1
		}

		offset := parseIntQueryParam(r, "offset", 0)
		if offset < 0 {
			offset = 0
		}

		// Check if database is available
		if db == nil {
			logger.Warn("database not available")
			RespondServiceUnavailable(w, "Database service not available")
			return
		}

		// Verify conversation exists
		_, err = db.GetConversation(ctx, id)
		if err != nil {
			logger.Warn("conversation not found", "id", id, "error", err)
			RespondNotFound(w, "Conversation not found")
			return
		}

		// Fetch messages
		messages, total, err := db.GetMessages(ctx, id, ListOptions{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			logger.Error("failed to fetch messages", "conversation_id", id, "error", err)
			RespondInternalError(w, "Failed to retrieve messages")
			return
		}

		if messages == nil {
			messages = []*Message{}
		}

		RespondJSON(w, http.StatusOK, PaginatedResponse{
			Data: messages,
			Pagination: Pagination{
				Total:   total,
				Limit:   limit,
				Offset:  offset,
				HasMore: offset+len(messages) < total,
			},
		})
	}
}

// parseIntQueryParam parses an integer query parameter with a default value.
func parseIntQueryParam(r *http.Request, name string, defaultValue int) int {
	value := r.URL.Query().Get(name)
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}
