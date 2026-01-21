// Package handlers provides HTTP request handlers for the API.
package handlers

import (
	"encoding/json"
	"net/http"
)

// APIError represents a structured API error response.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return e.Message
}

// Common API error codes.
const (
	ErrCodeBadRequest         = "BAD_REQUEST"
	ErrCodeUnauthorized       = "UNAUTHORIZED"
	ErrCodeForbidden          = "FORBIDDEN"
	ErrCodeNotFound           = "NOT_FOUND"
	ErrCodeMethodNotAllowed   = "METHOD_NOT_ALLOWED"
	ErrCodeConflict           = "CONFLICT"
	ErrCodeValidation         = "VALIDATION_ERROR"
	ErrCodeRateLimit          = "RATE_LIMIT_EXCEEDED"
	ErrCodeInternalError      = "INTERNAL_ERROR"
	ErrCodeServiceUnavailable = "SERVICE_UNAVAILABLE"
)

// NewAPIError creates a new APIError.
func NewAPIError(code, message string, details any) *APIError {
	return &APIError{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// ErrorResponse represents the standard error response format.
type ErrorResponse struct {
	Error *APIError `json:"error"`
}

// SuccessResponse represents a generic success response.
type SuccessResponse struct {
	Success bool `json:"success"`
	Data    any  `json:"data,omitempty"`
}

// PaginatedResponse represents a paginated response.
type PaginatedResponse struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// Pagination contains pagination metadata.
type Pagination struct {
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

// RespondJSON sends a JSON response with the given status code.
func RespondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			// Log error but can't do much else at this point
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
	}
}

// RespondError sends a JSON error response.
func RespondError(w http.ResponseWriter, status int, code, message string) {
	RespondJSON(w, status, ErrorResponse{
		Error: &APIError{
			Code:    code,
			Message: message,
		},
	})
}

// RespondErrorWithDetails sends a JSON error response with details.
func RespondErrorWithDetails(w http.ResponseWriter, status int, code, message string, details any) {
	RespondJSON(w, status, ErrorResponse{
		Error: &APIError{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// RespondSuccess sends a generic success response.
func RespondSuccess(w http.ResponseWriter, data any) {
	RespondJSON(w, http.StatusOK, SuccessResponse{
		Success: true,
		Data:    data,
	})
}

// RespondCreated sends a 201 Created response.
func RespondCreated(w http.ResponseWriter, data any) {
	RespondJSON(w, http.StatusCreated, data)
}

// RespondNoContent sends a 204 No Content response.
func RespondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// RespondBadRequest sends a 400 Bad Request response.
func RespondBadRequest(w http.ResponseWriter, message string) {
	RespondError(w, http.StatusBadRequest, ErrCodeBadRequest, message)
}

// RespondUnauthorized sends a 401 Unauthorized response.
func RespondUnauthorized(w http.ResponseWriter, message string) {
	if message == "" {
		message = "Authentication required"
	}
	RespondError(w, http.StatusUnauthorized, ErrCodeUnauthorized, message)
}

// RespondForbidden sends a 403 Forbidden response.
func RespondForbidden(w http.ResponseWriter, message string) {
	if message == "" {
		message = "Access denied"
	}
	RespondError(w, http.StatusForbidden, ErrCodeForbidden, message)
}

// RespondNotFound sends a 404 Not Found response.
func RespondNotFound(w http.ResponseWriter, message string) {
	if message == "" {
		message = "Resource not found"
	}
	RespondError(w, http.StatusNotFound, ErrCodeNotFound, message)
}

// RespondValidationError sends a 422 Unprocessable Entity response for validation errors.
func RespondValidationError(w http.ResponseWriter, details any) {
	RespondErrorWithDetails(w, http.StatusUnprocessableEntity, ErrCodeValidation, "Validation failed", details)
}

// RespondInternalError sends a 500 Internal Server Error response.
func RespondInternalError(w http.ResponseWriter, message string) {
	if message == "" {
		message = "An internal error occurred"
	}
	RespondError(w, http.StatusInternalServerError, ErrCodeInternalError, message)
}

// RespondServiceUnavailable sends a 503 Service Unavailable response.
func RespondServiceUnavailable(w http.ResponseWriter, message string) {
	if message == "" {
		message = "Service temporarily unavailable"
	}
	RespondError(w, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, message)
}
