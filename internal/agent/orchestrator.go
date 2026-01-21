// Package agent provides the AI agent orchestrator for ShariaComply AI.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/alqutdigital/islamic-banking-agent/internal/rag"
	"github.com/alqutdigital/islamic-banking-agent/internal/storage"
	"github.com/alqutdigital/islamic-banking-agent/internal/tools"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/uuid"
)

// DefaultSystemPrompt is the default system prompt for the ShariaComply AI agent.
const DefaultSystemPrompt = `You are ShariaComply AI, an expert assistant for Islamic banking compliance.

You help compliance officers, Shariah advisors, and product managers with:
- BNM (Bank Negara Malaysia) regulations and policy documents
- AAOIFI (Accounting and Auditing Organization for Islamic Financial Institutions) Shariah Standards
- Comparing Malaysian and international Islamic banking requirements

IMPORTANT RULES:
1. Always cite your sources with [Source N] markers that reference the search results
2. Be precise about which standard, document, or regulation you're referencing
3. If information is not in the provided context or search results, say so clearly - do not make up regulations
4. Never fabricate or hallucinate regulations, standards, or requirements
5. Distinguish clearly between BNM (Malaysian) and AAOIFI (international) requirements
6. When comparing standards, highlight both similarities and differences
7. If asked about recent updates, use the get_latest_circulars tool
8. For specific regulatory questions, search the appropriate source (BNM or AAOIFI)
9. When asked to compare, use the compare_standards tool

You have access to tools for searching regulations. Use them to find accurate information before responding.`

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
	client    *anthropic.Client
	retriever *rag.Retriever
	tools     *tools.Registry
	memory    *ConversationMemory
	config    OrchestratorConfig
	logger    *slog.Logger
	mu        sync.RWMutex
}

// NewOrchestrator creates a new agent orchestrator.
func NewOrchestrator(
	apiKey string,
	retriever *rag.Retriever,
	toolRegistry *tools.Registry,
	memory *ConversationMemory,
	logger *slog.Logger,
	config OrchestratorConfig,
) (*Orchestrator, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key is required")
	}

	if logger == nil {
		logger = slog.Default()
	}

	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)

	return &Orchestrator{
		client:    &client,
		retriever: retriever,
		tools:     toolRegistry,
		memory:    memory,
		config:    config,
		logger:    logger.With("component", "orchestrator"),
	}, nil
}

// Process handles a user request and returns a response.
func (o *Orchestrator) Process(ctx context.Context, req AgentRequest) (*AgentResponse, error) {
	startTime := time.Now()

	o.logger.Info("processing request",
		"conversation_id", req.ConversationID,
		"message_length", len(req.UserMessage),
	)

	// Ensure conversation exists
	if req.ConversationID == uuid.Nil {
		req.ConversationID = uuid.New()
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

	// Build messages for Claude
	messages := o.buildMessages(history, initialChunks, req.UserMessage)

	// Get tool definitions
	var toolDefs []anthropic.ToolUnionParam
	if o.tools != nil {
		for _, def := range o.tools.GetToolDefinitions() {
			toolDefs = append(toolDefs, anthropic.ToolUnionParam{
				OfTool: &anthropic.ToolParam{
					Name:        def.Name,
					Description: anthropic.String(def.Description),
					InputSchema: anthropic.ToolInputSchemaParam{
						Properties: def.InputSchema["properties"],
						ExtraFields: map[string]interface{}{
							"type":     def.InputSchema["type"],
							"required": def.InputSchema["required"],
						},
					},
				},
			})
		}
	}

	// Create initial request
	msgParams := anthropic.MessageNewParams{
		Model:     anthropic.Model(o.config.Model),
		MaxTokens: int64(o.config.MaxTokens),
		System: []anthropic.TextBlockParam{
			{Text: o.config.SystemPrompt},
		},
		Messages: messages,
	}

	if len(toolDefs) > 0 {
		msgParams.Tools = toolDefs
	}

	// Execute the agentic loop
	response, allToolCalls, err := o.executeAgenticLoop(ctx, msgParams)
	if err != nil {
		o.logger.Error("agent execution failed", "error", err)
		return nil, fmt.Errorf("agent execution failed: %w", err)
	}

	// Extract text response
	var textContent string
	for _, block := range response.Content {
		if block.Type == "text" {
			textContent += block.Text
		}
	}

	// Build response
	agentResponse := &AgentResponse{
		Answer:         textContent,
		ConversationID: req.ConversationID,
		Citations:      extractCitationsFromChunks(initialChunks),
		ToolCalls:      allToolCalls,
		RetrievedDocs:  initialChunks,
		TokensUsed: TokenUsage{
			InputTokens:  int(response.Usage.InputTokens),
			OutputTokens: int(response.Usage.OutputTokens),
			TotalTokens:  int(response.Usage.InputTokens + response.Usage.OutputTokens),
		},
		ProcessingTime: time.Since(startTime),
		Model:          string(response.Model),
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
			ModelUsed:      string(response.Model),
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
func (o *Orchestrator) executeAgenticLoop(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, []ToolCallInfo, error) {
	var allToolCalls []ToolCallInfo
	iteration := 0

	for iteration < o.config.MaxToolCalls {
		iteration++

		o.logger.Debug("executing LLM call", "iteration", iteration)

		// Make the API call
		response, err := o.client.Messages.New(ctx, params)
		if err != nil {
			return nil, allToolCalls, fmt.Errorf("Claude API call failed: %w", err)
		}

		// Check for tool use
		hasToolUse := false
		var toolUseBlocks []anthropic.ToolUseBlock
		for _, block := range response.Content {
			if block.Type == "tool_use" {
				hasToolUse = true
				toolUseBlocks = append(toolUseBlocks, block.AsToolUse())
			}
		}

		// If no tool use, we're done
		if !hasToolUse || response.StopReason != "tool_use" {
			return response, allToolCalls, nil
		}

		// Execute tool calls
		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range toolUseBlocks {
			o.logger.Debug("executing tool",
				"tool", block.Name,
				"id", block.ID,
			)

			// Track tool call
			toolCall := ToolCallInfo{
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			}
			allToolCalls = append(allToolCalls, toolCall)

			// Execute tool
			var resultContent string
			var isError bool

			if o.tools != nil {
				result, err := o.tools.Execute(ctx, block.Name, block.Input)
				if err != nil {
					resultContent = fmt.Sprintf("Error: %v", err)
					isError = true
					o.logger.Warn("tool execution failed",
						"tool", block.Name,
						"error", err,
					)
				} else {
					resultContent = result
				}
			} else {
				resultContent = "Error: Tool registry not configured"
				isError = true
			}

			// Build tool result
			toolResults = append(toolResults, anthropic.ContentBlockParamUnion{
				OfToolResult: &anthropic.ToolResultBlockParam{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content: []anthropic.ToolResultBlockParamContentUnion{
						{
							OfText: &anthropic.TextBlockParam{
								Type: "text",
								Text: resultContent,
							},
						},
					},
					IsError: anthropic.Bool(isError),
				},
			})
		}

		// Build assistant message with tool use
		var assistantContent []anthropic.ContentBlockParamUnion
		for _, block := range response.Content {
			if block.Type == "text" {
				assistantContent = append(assistantContent, anthropic.ContentBlockParamUnion{
					OfText: &anthropic.TextBlockParam{
						Type: "text",
						Text: block.Text,
					},
				})
			} else if block.Type == "tool_use" {
				toolUse := block.AsToolUse()
				assistantContent = append(assistantContent, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						Type:  "tool_use",
						ID:    toolUse.ID,
						Name:  toolUse.Name,
						Input: toolUse.Input,
					},
				})
			}
		}

		// Add assistant message and tool results to the conversation
		params.Messages = append(params.Messages, anthropic.MessageParam{
			Role:    "assistant",
			Content: assistantContent,
		})
		params.Messages = append(params.Messages, anthropic.MessageParam{
			Role:    "user",
			Content: toolResults,
		})
	}

	return nil, allToolCalls, fmt.Errorf("max tool calls (%d) exceeded", o.config.MaxToolCalls)
}

// buildMessages constructs the message array for Claude.
func (o *Orchestrator) buildMessages(history []Message, chunks []storage.RetrievedChunk, userMessage string) []anthropic.MessageParam {
	var messages []anthropic.MessageParam

	// Add conversation history
	for _, msg := range history {
		role := msg.Role
		if role == "system" {
			continue // System is handled separately
		}
		if role != "user" && role != "assistant" {
			continue
		}

		messages = append(messages, anthropic.MessageParam{
			Role: anthropic.MessageParamRole(role),
			Content: []anthropic.ContentBlockParamUnion{
				{
					OfText: &anthropic.TextBlockParam{
						Type: "text",
						Text: msg.Content,
					},
				},
			},
		})
	}

	// Build current message with optional context
	var userContent string
	if len(chunks) > 0 {
		userContent = buildContextString(chunks) + "\n\n" + userMessage
	} else {
		userContent = userMessage
	}

	messages = append(messages, anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlockParamUnion{
			{
				OfText: &anthropic.TextBlockParam{
					Type: "text",
					Text: userContent,
				},
			},
		},
	})

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
