// Package llm provides a unified interface for interacting with various LLM providers.
package llm

import (
	"context"
	"encoding/json"
)

// Provider defines the interface that all LLM providers must implement.
type Provider interface {
	// Chat sends a chat request to the LLM and returns the response.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// SupportsTools returns true if the provider supports native tool calling.
	SupportsTools() bool

	// Name returns the provider name (e.g., "anthropic", "ollama", "lmstudio").
	Name() string

	// Model returns the model name being used.
	Model() string
}

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonStop      StopReason = "stop"
)

// Role represents the role of a message sender.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// ContentType represents the type of content in a message.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
)

// ContentBlock represents a block of content in a message.
type ContentBlock struct {
	Type ContentType `json:"type"`

	// For text content
	Text string `json:"text,omitempty"`

	// For tool use content
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`

	// For tool result content
	ToolResult  string `json:"tool_result,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
}

// Message represents a message in the conversation.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// NewTextMessage creates a new text message.
func NewTextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: text},
		},
	}
}

// NewToolResultMessage creates a new tool result message.
func NewToolResultMessage(toolUseID, result string, isError bool) Message {
	return Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{
				Type:       ContentTypeToolResult,
				ToolUseID:  toolUseID,
				ToolResult: result,
				IsError:    isError,
			},
		},
	}
}

// GetText extracts all text content from a message.
func (m *Message) GetText() string {
	var text string
	for _, block := range m.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}

// GetToolCalls extracts all tool calls from a message.
func (m *Message) GetToolCalls() []ToolCall {
	var calls []ToolCall
	for _, block := range m.Content {
		if block.Type == ContentTypeToolUse {
			calls = append(calls, ToolCall{
				ID:    block.ToolUseID,
				Name:  block.ToolName,
				Input: block.ToolInput,
			})
		}
	}
	return calls
}

// ToolDefinition defines a tool that can be used by the LLM.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolCall represents a tool call made by the LLM.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ChatRequest represents a request to the LLM.
type ChatRequest struct {
	// Messages is the conversation history.
	Messages []Message `json:"messages"`

	// SystemPrompt is the system prompt for the conversation.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Tools is the list of tools available to the LLM.
	Tools []ToolDefinition `json:"tools,omitempty"`

	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature controls randomness in the response.
	Temperature float64 `json:"temperature,omitempty"`

	// StopSequences are sequences that will stop generation.
	StopSequences []string `json:"stop_sequences,omitempty"`
}

// ChatResponse represents a response from the LLM.
type ChatResponse struct {
	// Content contains the response content blocks.
	Content []ContentBlock `json:"content"`

	// StopReason indicates why the model stopped generating.
	StopReason StopReason `json:"stop_reason"`

	// Usage contains token usage information.
	Usage Usage `json:"usage"`

	// Model is the model that generated the response.
	Model string `json:"model"`
}

// GetText extracts all text content from a response.
func (r *ChatResponse) GetText() string {
	var text string
	for _, block := range r.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}

// GetToolCalls extracts all tool calls from a response.
func (r *ChatResponse) GetToolCalls() []ToolCall {
	var calls []ToolCall
	for _, block := range r.Content {
		if block.Type == ContentTypeToolUse {
			calls = append(calls, ToolCall{
				ID:    block.ToolUseID,
				Name:  block.ToolName,
				Input: block.ToolInput,
			})
		}
	}
	return calls
}

// HasToolCalls returns true if the response contains tool calls.
func (r *ChatResponse) HasToolCalls() bool {
	return r.StopReason == StopReasonToolUse || len(r.GetToolCalls()) > 0
}

// Usage contains token usage information.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// TotalTokens returns the total number of tokens used.
func (u Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}

// ProviderConfig holds common configuration for LLM providers.
type ProviderConfig struct {
	// Provider is the provider name (anthropic, ollama, lmstudio).
	Provider string `json:"provider"`

	// Model is the model to use.
	Model string `json:"model"`

	// APIKey is the API key for authentication (for Anthropic).
	APIKey string `json:"api_key,omitempty"`

	// BaseURL is the base URL for the API (for Ollama/LM Studio).
	BaseURL string `json:"base_url,omitempty"`

	// MaxTokens is the default maximum tokens to generate.
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature is the default temperature.
	Temperature float64 `json:"temperature,omitempty"`

	// EnableToolCalling enables or disables tool calling.
	EnableToolCalling bool `json:"enable_tool_calling,omitempty"`
}

// DefaultProviderConfig returns the default provider configuration.
func DefaultProviderConfig() ProviderConfig {
	return ProviderConfig{
		Provider:          "anthropic",
		Model:             "claude-sonnet-4-20250514",
		MaxTokens:         4096,
		Temperature:       0.3,
		EnableToolCalling: true,
	}
}
