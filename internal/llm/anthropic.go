package llm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider implements the Provider interface for Anthropic's Claude models.
type AnthropicProvider struct {
	client *anthropic.Client
	model  string
	logger *slog.Logger
	config ProviderConfig
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(cfg ProviderConfig, logger *slog.Logger) (*AnthropicProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Anthropic API key is required")
	}

	if logger == nil {
		logger = slog.Default()
	}

	client := anthropic.NewClient(
		option.WithAPIKey(cfg.APIKey),
	)

	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	return &AnthropicProvider{
		client: &client,
		model:  model,
		logger: logger.With("component", "anthropic_provider"),
		config: cfg,
	}, nil
}

// Chat sends a chat request to Claude and returns the response.
func (p *AnthropicProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to Anthropic format
	messages := p.convertMessages(req.Messages)

	// Build request params
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  messages,
	}

	// Set system prompt if provided
	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.SystemPrompt},
		}
	}

	// Set temperature if provided
	if req.Temperature > 0 {
		params.Temperature = anthropic.Float(req.Temperature)
	}

	// Convert and set tools if provided
	if len(req.Tools) > 0 && p.config.EnableToolCalling {
		params.Tools = p.convertToolDefinitions(req.Tools)
	}

	// Set stop sequences if provided
	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}

	p.logger.Debug("sending request to Anthropic",
		"model", p.model,
		"message_count", len(messages),
		"tool_count", len(req.Tools),
	)

	// Make the API call
	response, err := p.client.Messages.New(ctx, params)
	if err != nil {
		p.logger.Error("Anthropic API call failed", "error", err)
		return nil, fmt.Errorf("Anthropic API call failed: %w", err)
	}

	// Convert response to our format
	return p.convertResponse(response), nil
}

// SupportsTools returns true since Anthropic Claude supports native tool calling.
func (p *AnthropicProvider) SupportsTools() bool {
	return true
}

// Name returns the provider name.
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// Model returns the model name.
func (p *AnthropicProvider) Model() string {
	return p.model
}

// convertMessages converts our Message format to Anthropic's format.
func (p *AnthropicProvider) convertMessages(messages []Message) []anthropic.MessageParam {
	var result []anthropic.MessageParam

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			// System messages are handled separately in Anthropic
			continue
		}

		var content []anthropic.ContentBlockParamUnion
		for _, block := range msg.Content {
			switch block.Type {
			case ContentTypeText:
				content = append(content, anthropic.ContentBlockParamUnion{
					OfText: &anthropic.TextBlockParam{
						Type: "text",
						Text: block.Text,
					},
				})
			case ContentTypeToolUse:
				content = append(content, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						Type:  "tool_use",
						ID:    block.ToolUseID,
						Name:  block.ToolName,
						Input: block.ToolInput,
					},
				})
			case ContentTypeToolResult:
				content = append(content, anthropic.ContentBlockParamUnion{
					OfToolResult: &anthropic.ToolResultBlockParam{
						Type:      "tool_result",
						ToolUseID: block.ToolUseID,
						Content: []anthropic.ToolResultBlockParamContentUnion{
							{
								OfText: &anthropic.TextBlockParam{
									Type: "text",
									Text: block.ToolResult,
								},
							},
						},
						IsError: anthropic.Bool(block.IsError),
					},
				})
			}
		}

		result = append(result, anthropic.MessageParam{
			Role:    anthropic.MessageParamRole(msg.Role),
			Content: content,
		})
	}

	return result
}

// convertToolDefinitions converts our ToolDefinition format to Anthropic's format.
func (p *AnthropicProvider) convertToolDefinitions(tools []ToolDefinition) []anthropic.ToolUnionParam {
	var result []anthropic.ToolUnionParam

	for _, tool := range tools {
		result = append(result, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        tool.Name,
				Description: anthropic.String(tool.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: tool.InputSchema["properties"],
					ExtraFields: map[string]interface{}{
						"type":     tool.InputSchema["type"],
						"required": tool.InputSchema["required"],
					},
				},
			},
		})
	}

	return result
}

// convertResponse converts Anthropic's response to our ChatResponse format.
func (p *AnthropicProvider) convertResponse(resp *anthropic.Message) *ChatResponse {
	var content []ContentBlock

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content = append(content, ContentBlock{
				Type: ContentTypeText,
				Text: block.Text,
			})
		case "tool_use":
			toolUse := block.AsToolUse()
			content = append(content, ContentBlock{
				Type:      ContentTypeToolUse,
				ToolUseID: toolUse.ID,
				ToolName:  toolUse.Name,
				ToolInput: toolUse.Input,
			})
		}
	}

	return &ChatResponse{
		Content:    content,
		StopReason: p.convertStopReason(resp.StopReason),
		Usage: Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
		Model: string(resp.Model),
	}
}

// convertStopReason converts Anthropic's stop reason to our StopReason type.
func (p *AnthropicProvider) convertStopReason(reason anthropic.StopReason) StopReason {
	switch reason {
	case "end_turn":
		return StopReasonEndTurn
	case "tool_use":
		return StopReasonToolUse
	case "max_tokens":
		return StopReasonMaxTokens
	case "stop_sequence":
		return StopReasonStop
	default:
		return StopReasonEndTurn
	}
}

// BuildAssistantMessage creates an assistant message from a ChatResponse for use in follow-up requests.
func BuildAssistantMessage(resp *ChatResponse) Message {
	return Message{
		Role:    RoleAssistant,
		Content: resp.Content,
	}
}

// BuildToolResultMessages creates user messages containing tool results for a list of tool calls.
func BuildToolResultMessages(results []ToolResult) Message {
	var content []ContentBlock
	for _, result := range results {
		content = append(content, ContentBlock{
			Type:       ContentTypeToolResult,
			ToolUseID:  result.ToolUseID,
			ToolResult: result.Content,
			IsError:    result.IsError,
		})
	}
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}
