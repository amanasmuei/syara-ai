// Package handlers provides HTTP request handlers for the API.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================
// Mock Implementations
// ===========================

// MockDatabase implements Database interface for testing.
type MockDatabase struct {
	conversations    map[uuid.UUID]*Conversation
	messages         map[uuid.UUID][]*Message
	documents        map[uuid.UUID]*Document
	chunkVisuals     map[uuid.UUID]*ChunkVisual
	healthErr        error
	createErr        error
	updateErr        error
	deleteErr        error
	getConvErr       error
	getMsgsErr       error
	getDocErr        error
	getVisualErr     error
}

func NewMockDatabase() *MockDatabase {
	return &MockDatabase{
		conversations: make(map[uuid.UUID]*Conversation),
		messages:      make(map[uuid.UUID][]*Message),
		documents:     make(map[uuid.UUID]*Document),
		chunkVisuals:  make(map[uuid.UUID]*ChunkVisual),
	}
}

func (m *MockDatabase) Health(ctx context.Context) error {
	return m.healthErr
}

func (m *MockDatabase) CreateConversation(ctx context.Context, conv *Conversation) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.conversations[conv.ID] = conv
	return nil
}

func (m *MockDatabase) GetConversation(ctx context.Context, id uuid.UUID) (*Conversation, error) {
	if m.getConvErr != nil {
		return nil, m.getConvErr
	}
	conv, ok := m.conversations[id]
	if !ok {
		return nil, errors.New("conversation not found")
	}
	return conv, nil
}

func (m *MockDatabase) UpdateConversation(ctx context.Context, conv *Conversation) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if _, ok := m.conversations[conv.ID]; !ok {
		return errors.New("conversation not found")
	}
	m.conversations[conv.ID] = conv
	return nil
}

func (m *MockDatabase) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.conversations, id)
	return nil
}

func (m *MockDatabase) ListConversations(ctx context.Context, opts ListOptions) ([]*Conversation, int, error) {
	var result []*Conversation
	for _, conv := range m.conversations {
		result = append(result, conv)
	}
	total := len(result)

	// Apply pagination
	start := opts.Offset
	if start > len(result) {
		return []*Conversation{}, total, nil
	}
	end := start + opts.Limit
	if end > len(result) {
		end = len(result)
	}

	return result[start:end], total, nil
}

func (m *MockDatabase) CreateMessage(ctx context.Context, msg *Message) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.messages[msg.ConversationID] = append(m.messages[msg.ConversationID], msg)
	return nil
}

func (m *MockDatabase) GetMessages(ctx context.Context, conversationID uuid.UUID, opts ListOptions) ([]*Message, int, error) {
	if m.getMsgsErr != nil {
		return nil, 0, m.getMsgsErr
	}
	msgs := m.messages[conversationID]
	if msgs == nil {
		return []*Message{}, 0, nil
	}
	return msgs, len(msgs), nil
}

func (m *MockDatabase) GetDocument(ctx context.Context, id uuid.UUID) (*Document, error) {
	if m.getDocErr != nil {
		return nil, m.getDocErr
	}
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("document not found")
	}
	return doc, nil
}

func (m *MockDatabase) GetChunkVisual(ctx context.Context, chunkID uuid.UUID) (*ChunkVisual, error) {
	if m.getVisualErr != nil {
		return nil, m.getVisualErr
	}
	visual, ok := m.chunkVisuals[chunkID]
	if !ok {
		return nil, errors.New("chunk visual not found")
	}
	return visual, nil
}

// AddConversation adds a test conversation to the mock database.
func (m *MockDatabase) AddConversation(conv *Conversation) {
	m.conversations[conv.ID] = conv
}

// AddDocument adds a test document to the mock database.
func (m *MockDatabase) AddDocument(doc *Document) {
	m.documents[doc.ID] = doc
}

// MockObjectStorage implements ObjectStorage interface for testing.
type MockObjectStorage struct {
	healthErr    error
	signedURLErr error
	existsErr    error
	exists       bool
}

func NewMockObjectStorage() *MockObjectStorage {
	return &MockObjectStorage{
		exists: true,
	}
}

func (m *MockObjectStorage) Health(ctx context.Context) error {
	return m.healthErr
}

func (m *MockObjectStorage) GenerateSignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if m.signedURLErr != nil {
		return "", m.signedURLErr
	}
	return "https://storage.example.com/signed/" + path, nil
}

func (m *MockObjectStorage) Exists(ctx context.Context, path string) (bool, error) {
	if m.existsErr != nil {
		return false, m.existsErr
	}
	return m.exists, nil
}

// MockChatService implements ChatService interface for testing.
type MockChatService struct {
	result     *ChatResult
	processErr error
}

func NewMockChatService() *MockChatService {
	return &MockChatService{
		result: &ChatResult{
			ConversationID: uuid.New().String(),
			Answer:         "This is a test response about Islamic banking.",
			Citations:      []Citation{},
			Confidence:     0.95,
			TokensUsed:     100,
			ModelUsed:      "claude-3-test",
		},
	}
}

func (m *MockChatService) Process(ctx context.Context, req ChatRequest) (*ChatResult, error) {
	if m.processErr != nil {
		return nil, m.processErr
	}
	return m.result, nil
}

// ===========================
// Test Helper Functions
// ===========================

func testLogger() *slog.Logger {
	return slog.Default()
}

func createTestRouter(db Database, storage ObjectStorage, chatService ChatService) *chi.Mux {
	r := chi.NewRouter()
	logger := testLogger()

	// Health routes
	r.Get("/health", HealthCheck())
	r.Get("/ready", ReadyCheck(db, storage))

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/chat", HandleChat(chatService, logger))

		r.Route("/conversations", func(r chi.Router) {
			r.Get("/", ListConversations(db, logger))
			r.Post("/", CreateConversation(db, logger))
			r.Get("/{id}", GetConversation(db, logger))
			r.Put("/{id}", UpdateConversation(db, logger))
			r.Delete("/{id}", DeleteConversation(db, logger))
			r.Get("/{id}/messages", GetConversationMessages(db, logger))
		})
	})

	return r
}

// ===========================
// Health Check Tests
// ===========================

func TestHealthCheck(t *testing.T) {
	handler := HealthCheck()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response HealthStatus
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "healthy", response.Status)
	assert.Equal(t, "islamic-banking-agent", response.Service)
	assert.NotEmpty(t, response.Version)
	assert.NotEmpty(t, response.Timestamp)
}

func TestReadyCheck_AllHealthy(t *testing.T) {
	db := NewMockDatabase()
	storage := NewMockObjectStorage()

	handler := ReadyCheck(db, storage)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ReadyStatus
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "ready", response.Status)
	assert.Equal(t, "healthy", response.Components["database"])
	assert.Equal(t, "healthy", response.Components["object_storage"])
}

func TestReadyCheck_DatabaseUnhealthy(t *testing.T) {
	db := NewMockDatabase()
	db.healthErr = errors.New("connection refused")
	storage := NewMockObjectStorage()

	handler := ReadyCheck(db, storage)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var response ReadyStatus
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "not ready", response.Status)
	assert.Contains(t, response.Components["database"], "unhealthy")
}

func TestReadyCheck_NilDependencies(t *testing.T) {
	handler := ReadyCheck(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ReadyStatus
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "ready", response.Status)
	assert.Equal(t, "not configured", response.Components["database"])
	assert.Equal(t, "not configured", response.Components["object_storage"])
}

// ===========================
// Chat Handler Tests
// ===========================

func TestHandleChat_Success(t *testing.T) {
	chatService := NewMockChatService()
	router := createTestRouter(nil, nil, chatService)

	body := `{"message": "What is Islamic banking?"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ChatResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Contains(t, response.Answer, "Islamic banking")
	assert.NotEmpty(t, response.Metadata.ConversationID)
	assert.GreaterOrEqual(t, response.Metadata.Confidence, 0.0)
}

func TestHandleChat_InvalidJSON(t *testing.T) {
	chatService := NewMockChatService()
	router := createTestRouter(nil, nil, chatService)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewBufferString("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleChat_EmptyMessage(t *testing.T) {
	chatService := NewMockChatService()
	router := createTestRouter(nil, nil, chatService)

	body := `{"message": ""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestHandleChat_MessageTooLong(t *testing.T) {
	chatService := NewMockChatService()
	router := createTestRouter(nil, nil, chatService)

	// Create a message with > 2000 characters
	longMessage := make([]byte, 2001)
	for i := range longMessage {
		longMessage[i] = 'a'
	}

	body := `{"message": "` + string(longMessage) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestHandleChat_ServiceError(t *testing.T) {
	chatService := NewMockChatService()
	chatService.processErr = errors.New("LLM service unavailable")
	router := createTestRouter(nil, nil, chatService)

	body := `{"message": "What is Islamic banking?"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleChat_NilChatService(t *testing.T) {
	router := createTestRouter(nil, nil, nil)

	body := `{"message": "What is Islamic banking?"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ChatResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Contains(t, response.Answer, "not currently available")
}

// ===========================
// Conversation Handler Tests
// ===========================

func TestListConversations_Empty(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ConversationListResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Empty(t, response.Conversations)
	assert.Equal(t, 0, response.Pagination.Total)
}

func TestListConversations_WithData(t *testing.T) {
	db := NewMockDatabase()

	// Add test conversations
	now := time.Now()
	conv1 := &Conversation{
		ID:        uuid.New(),
		Title:     "Test Conversation 1",
		CreatedAt: now,
		UpdatedAt: now,
	}
	conv2 := &Conversation{
		ID:        uuid.New(),
		Title:     "Test Conversation 2",
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.AddConversation(conv1)
	db.AddConversation(conv2)

	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ConversationListResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Len(t, response.Conversations, 2)
	assert.Equal(t, 2, response.Pagination.Total)
}

func TestListConversations_Pagination(t *testing.T) {
	db := NewMockDatabase()

	// Add multiple conversations
	now := time.Now()
	for i := 0; i < 5; i++ {
		conv := &Conversation{
			ID:        uuid.New(),
			Title:     "Test Conversation",
			CreatedAt: now,
			UpdatedAt: now,
		}
		db.AddConversation(conv)
	}

	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations?limit=2&offset=0", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ConversationListResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Len(t, response.Conversations, 2)
	assert.Equal(t, 5, response.Pagination.Total)
	assert.True(t, response.Pagination.HasMore)
}

func TestListConversations_NilDatabase(t *testing.T) {
	router := createTestRouter(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestCreateConversation_Success(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	body := `{"title": "New Test Conversation"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var conv Conversation
	err := json.NewDecoder(rec.Body).Decode(&conv)
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, conv.ID)
	assert.Equal(t, "New Test Conversation", conv.Title)
}

func TestCreateConversation_EmptyTitle(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var conv Conversation
	err := json.NewDecoder(rec.Body).Decode(&conv)
	require.NoError(t, err)

	assert.Equal(t, "New Conversation", conv.Title) // Default title
}

func TestCreateConversation_InvalidJSON(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetConversation_Success(t *testing.T) {
	db := NewMockDatabase()

	now := time.Now()
	convID := uuid.New()
	conv := &Conversation{
		ID:        convID,
		Title:     "Test Conversation",
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.AddConversation(conv)

	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+convID.String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response ConversationWithMessages
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, convID, response.ID)
	assert.Equal(t, "Test Conversation", response.Title)
}

func TestGetConversation_NotFound(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetConversation_InvalidID(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/invalid-uuid", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateConversation_Success(t *testing.T) {
	db := NewMockDatabase()

	now := time.Now()
	convID := uuid.New()
	conv := &Conversation{
		ID:        convID,
		Title:     "Original Title",
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.AddConversation(conv)

	router := createTestRouter(db, nil, nil)

	body := `{"title": "Updated Title", "is_archived": true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/conversations/"+convID.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response Conversation
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "Updated Title", response.Title)
	assert.True(t, response.IsArchived)
}

func TestUpdateConversation_NotFound(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	body := `{"title": "Updated Title"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/conversations/"+uuid.New().String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteConversation_Success(t *testing.T) {
	db := NewMockDatabase()

	now := time.Now()
	convID := uuid.New()
	conv := &Conversation{
		ID:        convID,
		Title:     "Test Conversation",
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.AddConversation(conv)

	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/"+convID.String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDeleteConversation_NotFound(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetConversationMessages_Success(t *testing.T) {
	db := NewMockDatabase()

	now := time.Now()
	convID := uuid.New()
	conv := &Conversation{
		ID:        convID,
		Title:     "Test Conversation",
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.AddConversation(conv)

	// Add messages
	msg := &Message{
		ID:             uuid.New(),
		ConversationID: convID,
		Role:           "user",
		Content:        "Test message",
		CreatedAt:      now,
	}
	db.CreateMessage(context.Background(), msg)

	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+convID.String()+"/messages", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response PaginatedResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, 1, response.Pagination.Total)
}

func TestGetConversationMessages_NotFound(t *testing.T) {
	db := NewMockDatabase()
	router := createTestRouter(db, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+uuid.New().String()+"/messages", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ===========================
// Chat Request Validation Tests
// ===========================

func TestValidateChatRequest_Valid(t *testing.T) {
	req := &ChatRequestBody{
		Message: "What is Islamic banking?",
	}

	errors := ValidateChatRequest(req)
	assert.Empty(t, errors)
}

func TestValidateChatRequest_EmptyMessage(t *testing.T) {
	req := &ChatRequestBody{
		Message: "",
	}

	errors := ValidateChatRequest(req)
	assert.Len(t, errors, 1)
	assert.Equal(t, "message", errors[0].Field)
}

func TestValidateChatRequest_WhitespaceOnlyMessage(t *testing.T) {
	req := &ChatRequestBody{
		Message: "   ",
	}

	errors := ValidateChatRequest(req)
	assert.Len(t, errors, 1)
	assert.Equal(t, "message", errors[0].Field)
}

func TestValidateChatRequest_MessageTooLong(t *testing.T) {
	longMessage := make([]byte, 2001)
	for i := range longMessage {
		longMessage[i] = 'a'
	}

	req := &ChatRequestBody{
		Message: string(longMessage),
	}

	errors := ValidateChatRequest(req)
	assert.Len(t, errors, 1)
	assert.Equal(t, "message", errors[0].Field)
	assert.Contains(t, errors[0].Message, "2000")
}

func TestValidateChatRequest_InvalidConversationID(t *testing.T) {
	req := &ChatRequestBody{
		Message:        "Test message",
		ConversationID: "invalid-uuid",
	}

	errors := ValidateChatRequest(req)
	assert.Len(t, errors, 1)
	assert.Equal(t, "conversation_id", errors[0].Field)
}

func TestValidateChatRequest_ValidConversationID(t *testing.T) {
	req := &ChatRequestBody{
		Message:        "Test message",
		ConversationID: uuid.New().String(),
	}

	errors := ValidateChatRequest(req)
	assert.Empty(t, errors)
}

// ===========================
// Response Helper Tests
// ===========================

func TestRespondJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	data := map[string]string{"key": "value"}

	RespondJSON(rec, http.StatusOK, data)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var response map[string]string
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)
	assert.Equal(t, "value", response["key"])
}

func TestRespondError(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondError(rec, http.StatusBadRequest, ErrCodeBadRequest, "Test error")

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var response ErrorResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)
	assert.Equal(t, ErrCodeBadRequest, response.Error.Code)
	assert.Equal(t, "Test error", response.Error.Message)
}

func TestRespondCreated(t *testing.T) {
	rec := httptest.NewRecorder()
	data := map[string]string{"id": "123"}

	RespondCreated(rec, data)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestRespondNoContent(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondNoContent(rec)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

// ===========================
// Integration Test with Full Router
// ===========================

func TestFullAPIWorkflow(t *testing.T) {
	db := NewMockDatabase()
	storage := NewMockObjectStorage()
	chatService := NewMockChatService()
	router := createTestRouter(db, storage, chatService)

	// 1. Check health
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// 2. Check readiness
	req = httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// 3. Create conversation
	body := `{"title": "Test Workflow Conversation"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/conversations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)

	var conv Conversation
	err := json.NewDecoder(rec.Body).Decode(&conv)
	require.NoError(t, err)
	convID := conv.ID.String()

	// 4. Send chat message
	body = `{"message": "What is Islamic banking?", "conversation_id": "` + convID + `"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// 5. Get conversation
	req = httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+convID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// 6. Update conversation
	body = `{"title": "Updated Workflow Conversation"}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/conversations/"+convID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// 7. List conversations
	req = httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// 8. Delete conversation
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/"+convID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// 9. Verify deletion
	req = httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+convID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ===========================
// Benchmark Tests
// ===========================

func BenchmarkHealthCheck(b *testing.B) {
	handler := HealthCheck()

	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkListConversations(b *testing.B) {
	db := NewMockDatabase()
	logger := testLogger()
	handler := ListConversations(db, logger)

	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkChatValidation(b *testing.B) {
	req := &ChatRequestBody{
		Message:        "What is Islamic banking?",
		ConversationID: uuid.New().String(),
	}

	for i := 0; i < b.N; i++ {
		ValidateChatRequest(req)
	}
}
