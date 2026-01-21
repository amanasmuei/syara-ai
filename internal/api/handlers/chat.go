// Package handlers provides HTTP request handlers for the API.
package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// ChatRequestBody represents the incoming chat request body.
type ChatRequestBody struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// ChatResponse represents the chat API response.
type ChatResponse struct {
	Answer    string           `json:"answer"`
	Citations []Citation       `json:"citations"`
	Metadata  ResponseMetadata `json:"metadata"`
}

// ResponseMetadata contains metadata about the response.
type ResponseMetadata struct {
	ConversationID string  `json:"conversation_id"`
	SourcesUsed    int     `json:"sources_used"`
	Confidence     float64 `json:"confidence"`
	ProcessingTime int64   `json:"processing_time_ms"`
	TokensUsed     int     `json:"tokens_used,omitempty"`
	ModelUsed      string  `json:"model_used,omitempty"`
}

// ChatValidationError represents a validation error for chat requests.
type ChatValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidateChatRequest validates the chat request body.
func ValidateChatRequest(req *ChatRequestBody) []ChatValidationError {
	var errors []ChatValidationError

	// Validate message
	message := strings.TrimSpace(req.Message)
	if message == "" {
		errors = append(errors, ChatValidationError{
			Field:   "message",
			Message: "Message is required",
		})
	} else {
		// Check message length (1-2000 characters)
		runeCount := utf8.RuneCountInString(message)
		if runeCount < 1 {
			errors = append(errors, ChatValidationError{
				Field:   "message",
				Message: "Message must be at least 1 character",
			})
		} else if runeCount > 2000 {
			errors = append(errors, ChatValidationError{
				Field:   "message",
				Message: "Message must not exceed 2000 characters",
			})
		}
	}

	// Validate conversation_id if provided (should be a valid UUID format)
	if req.ConversationID != "" {
		// Simple UUID format validation
		if len(req.ConversationID) != 36 {
			errors = append(errors, ChatValidationError{
				Field:   "conversation_id",
				Message: "Invalid conversation ID format",
			})
		}
	}

	return errors
}

// HandleChat returns a handler for processing chat messages.
// POST /api/v1/chat
//
// Request body:
//
//	{
//	  "message": "What is Islamic banking?",
//	  "conversation_id": "optional-uuid"
//	}
//
// Response:
//
//	{
//	  "answer": "Islamic banking is...",
//	  "citations": [...],
//	  "metadata": {...}
//	}
func HandleChat(chatService ChatService, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		startTime := time.Now()

		// Parse request body
		var req ChatRequestBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn("failed to decode chat request", "error", err)
			RespondBadRequest(w, "Invalid request body")
			return
		}

		// Validate request
		if validationErrors := ValidateChatRequest(&req); len(validationErrors) > 0 {
			logger.Warn("chat request validation failed", "errors", validationErrors)
			RespondValidationError(w, validationErrors)
			return
		}

		// Trim message
		req.Message = strings.TrimSpace(req.Message)

		logger.Info("processing chat request",
			"conversation_id", req.ConversationID,
			"message_length", len(req.Message),
		)

		// Check if chat service is available
		if chatService == nil {
			logger.Warn("chat service not available")
			// Return a placeholder response when service is not configured
			RespondJSON(w, http.StatusOK, ChatResponse{
				Answer:    "Chat service is not currently available. Please try again later.",
				Citations: []Citation{},
				Metadata: ResponseMetadata{
					ConversationID: req.ConversationID,
					SourcesUsed:    0,
					Confidence:     0,
					ProcessingTime: time.Since(startTime).Milliseconds(),
				},
			})
			return
		}

		// Process chat message
		result, err := chatService.Process(ctx, ChatRequest{
			ConversationID: req.ConversationID,
			Message:        req.Message,
		})
		if err != nil {
			logger.Error("failed to process chat message",
				"error", err,
				"conversation_id", req.ConversationID,
			)
			RespondInternalError(w, "Failed to process your message. Please try again.")
			return
		}

		// Build response
		response := ChatResponse{
			Answer:    result.Answer,
			Citations: result.Citations,
			Metadata: ResponseMetadata{
				ConversationID: result.ConversationID,
				SourcesUsed:    len(result.Citations),
				Confidence:     result.Confidence,
				ProcessingTime: time.Since(startTime).Milliseconds(),
				TokensUsed:     result.TokensUsed,
				ModelUsed:      result.ModelUsed,
			},
		}

		// Ensure citations is not nil
		if response.Citations == nil {
			response.Citations = []Citation{}
		}

		logger.Info("chat request completed",
			"conversation_id", result.ConversationID,
			"sources_used", len(result.Citations),
			"processing_time_ms", response.Metadata.ProcessingTime,
		)

		RespondJSON(w, http.StatusOK, response)
	}
}
