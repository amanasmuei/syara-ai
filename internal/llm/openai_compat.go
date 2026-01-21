package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/sashabaranov/go-openai"
)

// OpenAICompatProvider implements the Provider interface for OpenAI-compatible APIs.
// This works with Ollama, LM Studio, and other OpenAI-compatible servers.
type OpenAICompatProvider struct {
	client       *openai.Client
	model        string
	providerName string // "ollama" or "lmstudio"
	logger       *slog.Logger
	config       ProviderConfig
}

// NewOpenAICompatProvider creates a new OpenAI-compatible provider.
func NewOpenAICompatProvider(cfg ProviderConfig, logger *slog.Logger) (*OpenAICompatProvider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required for OpenAI-compatible provider")
	}

	if logger == nil {
		logger = slog.Default()
	}

	// Create client config with custom base URL
	// Use a dummy API key for local servers that don't require authentication
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "not-needed" // Local servers like Ollama/LM Studio don't require API keys
	}

	clientConfig := openai.DefaultConfig(apiKey)
	clientConfig.BaseURL = cfg.BaseURL

	client := openai.NewClientWithConfig(clientConfig)

	model := cfg.Model
	if model == "" {
		model = "llama3.2" // Default for Ollama
	}

	providerName := cfg.Provider
	if providerName == "" {
		providerName = "openai_compat"
	}

	return &OpenAICompatProvider{
		client:       client,
		model:        model,
		providerName: providerName,
		logger:       logger.With("component", "openai_compat_provider", "provider", providerName),
		config:       cfg,
	}, nil
}

// NewOllamaProvider creates a new Ollama provider with default settings.
func NewOllamaProvider(baseURL, model string, logger *slog.Logger) (*OpenAICompatProvider, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	if model == "" {
		model = "llama3.2"
	}

	return NewOpenAICompatProvider(ProviderConfig{
		Provider:          "ollama",
		BaseURL:           baseURL,
		Model:             model,
		EnableToolCalling: true,
	}, logger)
}

// NewLMStudioProvider creates a new LM Studio provider with default settings.
func NewLMStudioProvider(baseURL, model string, logger *slog.Logger) (*OpenAICompatProvider, error) {
	if baseURL == "" {
		baseURL = "http://localhost:1234/v1"
	}
	if model == "" {
		model = "local-model"
	}

	return NewOpenAICompatProvider(ProviderConfig{
		Provider:          "lmstudio",
		BaseURL:           baseURL,
		Model:             model,
		EnableToolCalling: true,
	}, logger)
}

// Chat sends a chat request to the OpenAI-compatible server and returns the response.
func (p *OpenAICompatProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to OpenAI format
	messages := p.convertMessages(req)

	// Build request
	chatReq := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: messages,
	}

	// Set max tokens if provided
	if req.MaxTokens > 0 {
		chatReq.MaxTokens = req.MaxTokens
	}

	// Set temperature if provided
	if req.Temperature > 0 {
		chatReq.Temperature = float32(req.Temperature)
	}

	// Set stop sequences if provided
	if len(req.StopSequences) > 0 {
		chatReq.Stop = req.StopSequences
	}

	// Convert and set tools if provided and enabled
	if len(req.Tools) > 0 && p.config.EnableToolCalling {
		chatReq.Tools = p.convertToolDefinitions(req.Tools)
	}

	p.logger.Debug("sending request to OpenAI-compatible server",
		"model", p.model,
		"base_url", p.config.BaseURL,
		"message_count", len(messages),
		"tool_count", len(req.Tools),
	)

	// Make the API call
	response, err := p.client.CreateChatCompletion(ctx, chatReq)
	if err != nil {
		p.logger.Error("OpenAI-compatible API call failed", "error", err)
		return nil, fmt.Errorf("OpenAI-compatible API call failed: %w", err)
	}

	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from OpenAI-compatible API")
	}

	// Convert response to our format
	return p.convertResponse(&response), nil
}

// SupportsTools returns true if tool calling is enabled for this provider.
// Note: Actual tool support depends on the model being used.
func (p *OpenAICompatProvider) SupportsTools() bool {
	return p.config.EnableToolCalling
}

// Name returns the provider name.
func (p *OpenAICompatProvider) Name() string {
	return p.providerName
}

// Model returns the model name.
func (p *OpenAICompatProvider) Model() string {
	return p.model
}

// convertMessages converts our Message format to OpenAI's format.
func (p *OpenAICompatProvider) convertMessages(req ChatRequest) []openai.ChatCompletionMessage {
	var result []openai.ChatCompletionMessage

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		result = append(result, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleSystem:
			result = append(result, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: msg.GetText(),
			})
		case RoleUser:
			// Check if this is a tool result message
			if len(msg.Content) > 0 && msg.Content[0].Type == ContentTypeToolResult {
				for _, block := range msg.Content {
					if block.Type == ContentTypeToolResult {
						result = append(result, openai.ChatCompletionMessage{
							Role:       openai.ChatMessageRoleTool,
							Content:    block.ToolResult,
							ToolCallID: block.ToolUseID,
						})
					}
				}
			} else {
				result = append(result, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: msg.GetText(),
				})
			}
		case RoleAssistant:
			assistantMsg := openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: msg.GetText(),
			}

			// Add tool calls if present
			toolCalls := msg.GetToolCalls()
			if len(toolCalls) > 0 {
				for _, tc := range toolCalls {
					assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, openai.ToolCall{
						ID:   tc.ID,
						Type: openai.ToolTypeFunction,
						Function: openai.FunctionCall{
							Name:      tc.Name,
							Arguments: string(tc.Input),
						},
					})
				}
			}

			result = append(result, assistantMsg)
		}
	}

	return result
}

// convertToolDefinitions converts our ToolDefinition format to OpenAI's format.
func (p *OpenAICompatProvider) convertToolDefinitions(tools []ToolDefinition) []openai.Tool {
	var result []openai.Tool

	for _, tool := range tools {
		// Convert input schema to JSON for OpenAI
		schemaJSON, err := json.Marshal(tool.InputSchema)
		if err != nil {
			p.logger.Warn("failed to marshal tool schema", "tool", tool.Name, "error", err)
			continue
		}

		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  json.RawMessage(schemaJSON),
			},
		})
	}

	return result
}

// convertResponse converts OpenAI's response to our ChatResponse format.
func (p *OpenAICompatProvider) convertResponse(resp *openai.ChatCompletionResponse) *ChatResponse {
	choice := resp.Choices[0]
	var content []ContentBlock

	// Add text content if present
	if choice.Message.Content != "" {
		content = append(content, ContentBlock{
			Type: ContentTypeText,
			Text: choice.Message.Content,
		})
	}

	// Add tool calls if present
	for _, tc := range choice.Message.ToolCalls {
		content = append(content, ContentBlock{
			Type:      ContentTypeToolUse,
			ToolUseID: tc.ID,
			ToolName:  tc.Function.Name,
			ToolInput: json.RawMessage(tc.Function.Arguments),
		})
	}

	return &ChatResponse{
		Content:    content,
		StopReason: p.convertFinishReason(choice.FinishReason),
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
		Model: resp.Model,
	}
}

// convertFinishReason converts OpenAI's finish reason to our StopReason type.
func (p *OpenAICompatProvider) convertFinishReason(reason openai.FinishReason) StopReason {
	switch reason {
	case openai.FinishReasonStop:
		return StopReasonEndTurn
	case openai.FinishReasonToolCalls:
		return StopReasonToolUse
	case openai.FinishReasonLength:
		return StopReasonMaxTokens
	default:
		return StopReasonEndTurn
	}
}
