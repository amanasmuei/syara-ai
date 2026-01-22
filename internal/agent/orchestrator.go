// Package agent provides the AI agent orchestrator for ShariaComply AI.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/llm"
	"github.com/alqutdigital/islamic-banking-agent/internal/rag"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/internal/tools"
	"github.com/google/uuid"
)

// DefaultSystemPrompt is the default system prompt for the SyaRA AI agent.
const DefaultSystemPrompt = `You are SyaRA (Shariah Regulatory Assistant), an expert AI assistant specializing in Shariah regulatory guidance and Islamic finance compliance.

You help Shariah scholars, compliance officers, regulators, legal professionals, and financial institutions with:
- BNM (Bank Negara Malaysia) regulations and policy documents for Islamic banking
- SC Malaysia (Securities Commission) guidelines for Islamic capital markets and sukuk
- AAOIFI (Accounting and Auditing Organization for Islamic Financial Institutions) Shariah Standards
- IIFA (International Islamic Fiqh Academy / Majma Fiqh) resolutions on contemporary fiqh issues
- Malaysian State Fatwa Authority rulings from various state fatwa committees
- Cross-jurisdictional regulatory comparisons

KEY CAPABILITIES:
- Search and retrieve specific regulations, standards, and fatwas from authoritative sources
- Compare requirements across different regulatory frameworks (Malaysian vs international)
- Provide guidance on Shariah compliance requirements for financial products
- Explain fiqh rulings and their practical applications in Islamic finance
- Track latest regulatory circulars and updates

IMPORTANT RULES:
1. Always cite your sources with [Source N] markers that reference the search results
2. Be precise about which standard, document, regulation, or fatwa you're referencing
3. If information is not in the provided context or search results, say so clearly - do not fabricate regulations or rulings
4. Never hallucinate fatwas, standards, or regulatory requirements
5. Distinguish clearly between different regulatory sources:
   - BNM: Malaysian central bank regulations
   - SC: Malaysian capital market regulations
   - AAOIFI: International Shariah standards
   - IIFA: International fiqh academy resolutions
   - State Fatwa: Malaysian state-level religious rulings
6. When comparing standards, highlight both similarities and differences
7. Use the appropriate search tool based on the query context:
   - search_bnm_regulations: For Malaysian Islamic banking regulations
   - search_sc_regulations: For Islamic capital market and sukuk guidelines
   - search_aaoifi_standards: For international Shariah standards
   - search_iifa_resolutions: For international fiqh rulings
   - search_state_fatwa: For Malaysian state fatwa rulings
8. Use get_latest_circulars for recent regulatory updates
9. Use compare_standards when asked to compare across frameworks

You have access to comprehensive regulatory search tools. Always search for accurate information before responding.`

// OrchestratorConfig holds configuration for the agent orchestrator.
type OrchestratorConfig struct {
	Model            string
	MaxTokens        int
	Temperature      float64
	SystemPrompt     string
	MaxToolCalls     int           // Maximum tool calls per request
	RequestTimeout   time.Duration // Timeout for LLM requests
	EnableStreaming  bool
	InitialRetrieval bool // Perform initial RAG retrieval before LLM call
	InitialTopK      int  // Number of documents for initial retrieval
}

// DefaultOrchestratorConfig returns the default orchestrator configuration.
func DefaultOrchestratorConfig() OrchestratorConfig {
	return OrchestratorConfig{
		Model:            "claude-sonnet-4-20250514",
		MaxTokens:        4096,
		Temperature:      0.3,
		SystemPrompt:     DefaultSystemPrompt,
		MaxToolCalls:     10,
		RequestTimeout:   120 * time.Second,
		EnableStreaming:  false,
		InitialRetrieval: true,
		InitialTopK:      5,
	}
}

// AgentRequest represents a request to the agent.
type AgentRequest struct {
	ConversationID uuid.UUID              `json:"conversation_id"`
	UserMessage    string                 `json:"user_message"`
	Context        map[string]interface{} `json:"context,omitempty"`
}

// AgentResponse represents a response from the agent.
type AgentResponse struct {
	Answer         string                   `json:"answer"`
	ConversationID uuid.UUID                `json:"conversation_id"`
	Citations      []Citation               `json:"citations,omitempty"`
	ToolCalls      []ToolCallInfo           `json:"tool_calls,omitempty"`
	RetrievedDocs  []storage.RetrievedChunk `json:"retrieved_docs,omitempty"`
	TokensUsed     TokenUsage               `json:"tokens_used"`
	ProcessingTime time.Duration            `json:"processing_time_ms"`
	Model          string                   `json:"model"`
}

// TokenUsage tracks token usage for billing.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Orchestrator is the main AI agent orchestrator.
type Orchestrator struct {
	provider  llm.Provider
	retriever *rag.Retriever
	tools     *tools.Registry
	memory    *ConversationMemory
	config    OrchestratorConfig
	logger    *slog.Logger
	mu        sync.RWMutex
}

// NewOrchestrator creates a new agent orchestrator with a provider.
func NewOrchestrator(
	provider llm.Provider,
	retriever *rag.Retriever,
	toolRegistry *tools.Registry,
	memory *ConversationMemory,
	logger *slog.Logger,
	config OrchestratorConfig,
) (*Orchestrator, error) {
	if provider == nil {
		return nil, fmt.Errorf("LLM provider is required")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &Orchestrator{
		provider:  provider,
		retriever: retriever,
		tools:     toolRegistry,
		memory:    memory,
		config:    config,
		logger:    logger.With("component", "orchestrator", "provider", provider.Name()),
	}, nil
}

// Process handles a user request and returns a response.
func (o *Orchestrator) Process(ctx context.Context, req AgentRequest) (*AgentResponse, error) {
	startTime := time.Now()

	o.logger.Info("processing request",
		"conversation_id", req.ConversationID,
		"message_length", len(req.UserMessage),
		"provider", o.provider.Name(),
		"model", o.provider.Model(),
	)

	// Ensure conversation exists
	isNewConversation := req.ConversationID == uuid.Nil
	if isNewConversation {
		req.ConversationID = uuid.New()
	}

	// Create conversation record if new
	if isNewConversation && o.memory != nil {
		// Extract first part of message as title (max 50 chars)
		title := req.UserMessage
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		if err := o.memory.EnsureConversation(ctx, req.ConversationID, title); err != nil {
			o.logger.Warn("failed to create conversation record", "error", err)
		}
	}

	// Get conversation history
	var history []Message
	if o.memory != nil {
		var err error
		history, err = o.memory.GetContextMessages(ctx, req.ConversationID, o.config.MaxTokens/2)
		if err != nil {
			o.logger.Warn("failed to get conversation history", "error", err)
		}
	}

	// Optional: Perform initial retrieval for context
	var initialChunks []storage.RetrievedChunk
	if o.config.InitialRetrieval && o.retriever != nil {
		result, err := o.retriever.Retrieve(ctx, req.UserMessage, rag.RetrievalOptions{
			TopK: o.config.InitialTopK,
		})
		if err != nil {
			o.logger.Warn("initial retrieval failed", "error", err)
		} else if result != nil {
			initialChunks = result.Chunks
		}
	}

	// Build messages for LLM
	messages := o.buildMessages(history, initialChunks, req.UserMessage)

	// Get tool definitions
	var toolDefs []llm.ToolDefinition
	if o.tools != nil && o.provider.SupportsTools() {
		for _, def := range o.tools.GetToolDefinitions() {
			toolDefs = append(toolDefs, llm.ToolDefinition{
				Name:        def.Name,
				Description: def.Description,
				InputSchema: def.InputSchema,
			})
		}
	}

	// Create initial request
	chatReq := llm.ChatRequest{
		Messages:     messages,
		SystemPrompt: o.config.SystemPrompt,
		Tools:        toolDefs,
		MaxTokens:    o.config.MaxTokens,
		Temperature:  o.config.Temperature,
	}

	// Execute the agentic loop
	response, allToolCalls, err := o.executeAgenticLoop(ctx, chatReq)
	if err != nil {
		o.logger.Error("agent execution failed", "error", err)
		return nil, fmt.Errorf("agent execution failed: %w", err)
	}

	// Extract text response
	textContent := response.GetText()

	// Build response
	agentResponse := &AgentResponse{
		Answer:         textContent,
		ConversationID: req.ConversationID,
		Citations:      extractCitationsFromChunks(initialChunks),
		ToolCalls:      allToolCalls,
		RetrievedDocs:  initialChunks,
		TokensUsed: TokenUsage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
			TotalTokens:  response.Usage.TotalTokens(),
		},
		ProcessingTime: time.Since(startTime),
		Model:          response.Model,
	}

	// Save to memory
	if o.memory != nil {
		userMsg := Message{
			ID:             uuid.New(),
			ConversationID: req.ConversationID,
			Role:           "user",
			Content:        req.UserMessage,
			CreatedAt:      startTime,
		}
		if err := o.memory.SaveMessage(ctx, userMsg); err != nil {
			o.logger.Warn("failed to save user message", "error", err)
		}

		assistantMsg := Message{
			ID:             uuid.New(),
			ConversationID: req.ConversationID,
			Role:           "assistant",
			Content:        textContent,
			Citations:      agentResponse.Citations,
			ToolCalls:      allToolCalls,
			TokensUsed:     agentResponse.TokensUsed.TotalTokens,
			ModelUsed:      response.Model,
			LatencyMs:      int(time.Since(startTime).Milliseconds()),
			CreatedAt:      time.Now(),
		}
		if err := o.memory.SaveMessage(ctx, assistantMsg); err != nil {
			o.logger.Warn("failed to save assistant message", "error", err)
		}
	}

	o.logger.Info("request processed",
		"conversation_id", req.ConversationID,
		"tool_calls", len(allToolCalls),
		"input_tokens", agentResponse.TokensUsed.InputTokens,
		"output_tokens", agentResponse.TokensUsed.OutputTokens,
		"processing_time_ms", agentResponse.ProcessingTime.Milliseconds(),
	)

	return agentResponse, nil
}

// executeAgenticLoop runs the agentic loop until completion or max iterations.
func (o *Orchestrator) executeAgenticLoop(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, []ToolCallInfo, error) {
	var allToolCalls []ToolCallInfo
	iteration := 0

	for iteration < o.config.MaxToolCalls {
		iteration++

		o.logger.Debug("executing LLM call", "iteration", iteration)

		// Make the API call
		response, err := o.provider.Chat(ctx, req)
		if err != nil {
			return nil, allToolCalls, fmt.Errorf("LLM API call failed: %w", err)
		}

		// Check for tool use
		if !response.HasToolCalls() {
			return response, allToolCalls, nil
		}

		// Get tool calls from response
		toolCalls := response.GetToolCalls()

		// Execute tool calls
		var toolResults []llm.ToolResult
		for _, tc := range toolCalls {
			o.logger.Debug("executing tool",
				"tool", tc.Name,
				"id", tc.ID,
			)

			// Track tool call
			toolCallInfo := ToolCallInfo{
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Input,
			}
			allToolCalls = append(allToolCalls, toolCallInfo)

			// Execute tool
			var resultContent string
			var isError bool

			if o.tools != nil {
				result, execErr := o.tools.Execute(ctx, tc.Name, tc.Input)
				if execErr != nil {
					resultContent = fmt.Sprintf("Error: %v", execErr)
					isError = true
					o.logger.Warn("tool execution failed",
						"tool", tc.Name,
						"error", execErr,
					)
				} else {
					resultContent = result
				}
			} else {
				resultContent = "Error: Tool registry not configured"
				isError = true
			}

			toolResults = append(toolResults, llm.ToolResult{
				ToolUseID: tc.ID,
				Content:   resultContent,
				IsError:   isError,
			})
		}

		// Add assistant message with tool use to the conversation
		req.Messages = append(req.Messages, llm.BuildAssistantMessage(response))

		// Add tool results as user message
		req.Messages = append(req.Messages, llm.BuildToolResultMessages(toolResults))
	}

	return nil, allToolCalls, fmt.Errorf("max tool calls (%d) exceeded", o.config.MaxToolCalls)
}

// buildMessages constructs the message array for the LLM.
func (o *Orchestrator) buildMessages(history []Message, chunks []storage.RetrievedChunk, userMessage string) []llm.Message {
	var messages []llm.Message

	// Add conversation history
	for _, msg := range history {
		role := msg.Role
		if role == "system" {
			continue // System is handled separately
		}
		if role != "user" && role != "assistant" {
			continue
		}

		messages = append(messages, llm.NewTextMessage(llm.Role(role), msg.Content))
	}

	// Build current message with optional context
	var userContent string
	if len(chunks) > 0 {
		userContent = buildContextString(chunks) + "\n\n" + userMessage
	} else {
		userContent = userMessage
	}

	messages = append(messages, llm.NewTextMessage(llm.RoleUser, userContent))

	return messages
}

// buildContextString builds a context string from retrieved chunks.
func buildContextString(chunks []storage.RetrievedChunk) string {
	if len(chunks) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Relevant Context\n\n")
	sb.WriteString("The following information was retrieved from the regulatory database:\n\n")

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("### [Source %d] ", i+1))

		// Source and title
		if chunk.SourceType != "" {
			sb.WriteString(fmt.Sprintf("%s: ", strings.ToUpper(chunk.SourceType)))
		}
		sb.WriteString(chunk.DocumentTitle)
		sb.WriteString("\n")

		// Section and page
		if chunk.SectionTitle != "" {
			sb.WriteString(fmt.Sprintf("Section: %s\n", chunk.SectionTitle))
		}
		if chunk.PageNumber > 0 {
			sb.WriteString(fmt.Sprintf("Page: %d\n", chunk.PageNumber))
		}
		if chunk.StandardNum != "" {
			sb.WriteString(fmt.Sprintf("Standard: %s\n", chunk.StandardNum))
		}

		sb.WriteString(fmt.Sprintf("\n%s\n\n", chunk.Content))
	}

	sb.WriteString("---\n\n")
	return sb.String()
}

// extractCitationsFromChunks creates citations from retrieved chunks.
func extractCitationsFromChunks(chunks []storage.RetrievedChunk) []Citation {
	citations := make([]Citation, len(chunks))

	for i, chunk := range chunks {
		citations[i] = Citation{
			Index:      i + 1,
			ChunkID:    chunk.ID,
			DocumentID: chunk.DocumentID,
			Source:     chunk.SourceType,
			Title:      chunk.DocumentTitle,
			Page:       chunk.PageNumber,
			Section:    chunk.SectionTitle,
			Content:    truncateForCitation(chunk.Content, 200),
			Similarity: chunk.Similarity,
		}
	}

	return citations
}

// truncateForCitation truncates content for citation display.
func truncateForCitation(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// Find word boundary
	truncated := content[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen-30 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// SetSystemPrompt updates the system prompt.
func (o *Orchestrator) SetSystemPrompt(prompt string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.config.SystemPrompt = prompt
}

// GetConfig returns the current configuration.
func (o *Orchestrator) GetConfig() OrchestratorConfig {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.config
}

// UpdateConfig updates the orchestrator configuration.
func (o *Orchestrator) UpdateConfig(config OrchestratorConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.config = config
}

// Provider returns the current LLM provider.
func (o *Orchestrator) Provider() llm.Provider {
	return o.provider
}
