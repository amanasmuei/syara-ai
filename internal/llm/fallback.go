package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ToolCallFallback provides fallback mechanisms for models that don't support native tool calling.
// It uses prompt injection to describe tools and parses tool calls from the model's text output.

const toolCallBlockStart = "```tool_call"
const toolCallBlockEnd = "```"

// ToolInstructionTemplate is the template for injecting tool instructions into the system prompt.
const ToolInstructionTemplate = `

## Available Tools

You have access to the following tools. When you need to use a tool, respond with a tool_call block in this exact format:

%s

### Tool Definitions

%s

### Important Rules for Tool Usage

1. Only use tools when necessary to answer the user's question
2. You can call multiple tools if needed - each in its own tool_call block
3. Wait for tool results before providing your final answer
4. If a tool returns an error, explain the issue and try an alternative approach
5. Always format tool calls exactly as shown above

`

// ToolCallJSONFormat shows the expected JSON format for tool calls.
const ToolCallJSONFormat = "```tool_call\n{\"name\": \"tool_name\", \"input\": {\"param1\": \"value1\"}}\n```"

// InjectToolsIntoPrompt adds tool descriptions and usage instructions to the system prompt.
// This is used when the model doesn't support native tool calling.
func InjectToolsIntoPrompt(systemPrompt string, tools []ToolDefinition) string {
	if len(tools) == 0 {
		return systemPrompt
	}

	var toolDescriptions strings.Builder
	for i, tool := range tools {
		toolDescriptions.WriteString(fmt.Sprintf("### %d. %s\n", i+1, tool.Name))
		toolDescriptions.WriteString(fmt.Sprintf("**Description**: %s\n\n", tool.Description))

		// Format input schema
		if tool.InputSchema != nil {
			schemaJSON, err := json.MarshalIndent(tool.InputSchema, "", "  ")
			if err == nil {
				toolDescriptions.WriteString("**Input Schema**:\n```json\n")
				toolDescriptions.WriteString(string(schemaJSON))
				toolDescriptions.WriteString("\n```\n\n")
			}
		}

		// Add example usage
		toolDescriptions.WriteString("**Example**:\n")
		toolDescriptions.WriteString(fmt.Sprintf("```tool_call\n{\"name\": \"%s\", \"input\": {", tool.Name))

		// Generate example inputs from schema
		if props, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
			examples := []string{}
			for propName := range props {
				examples = append(examples, fmt.Sprintf("\"%s\": \"...\"", propName))
			}
			toolDescriptions.WriteString(strings.Join(examples, ", "))
		}
		toolDescriptions.WriteString("}}\n```\n\n")
	}

	toolInstructions := fmt.Sprintf(ToolInstructionTemplate, ToolCallJSONFormat, toolDescriptions.String())
	return systemPrompt + toolInstructions
}

// FallbackToolCall represents a parsed tool call from text.
type FallbackToolCall struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ParseToolCallsFromText extracts tool calls from the model's text response.
// Returns the extracted tool calls and the remaining text (with tool_call blocks removed).
func ParseToolCallsFromText(content string) ([]ToolCall, string) {
	var toolCalls []ToolCall
	remainingText := content

	// Regex to find tool_call blocks
	// Match ```tool_call followed by JSON until closing ```
	pattern := regexp.MustCompile("(?s)```tool_call\\s*\\n(.*?)\\n```")
	matches := pattern.FindAllStringSubmatch(content, -1)

	for i, match := range matches {
		if len(match) < 2 {
			continue
		}

		jsonContent := strings.TrimSpace(match[1])

		var parsed FallbackToolCall
		if err := json.Unmarshal([]byte(jsonContent), &parsed); err != nil {
			// Try to be more lenient with JSON parsing
			continue
		}

		if parsed.Name == "" {
			continue
		}

		toolCall := ToolCall{
			ID:    fmt.Sprintf("fallback_%d", i),
			Name:  parsed.Name,
			Input: parsed.Input,
		}
		toolCalls = append(toolCalls, toolCall)

		// Remove this tool_call block from the remaining text
		remainingText = strings.Replace(remainingText, match[0], "", 1)
	}

	// Clean up the remaining text
	remainingText = strings.TrimSpace(remainingText)

	return toolCalls, remainingText
}

// FormatToolResultsForPrompt formats tool results to be included in a follow-up message.
// This is used to provide tool results back to the model in text format.
func FormatToolResultsForPrompt(results []ToolResult) string {
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Tool Results\n\n")

	for _, result := range results {
		sb.WriteString(fmt.Sprintf("### Result for tool call `%s`\n\n", result.ToolUseID))
		if result.IsError {
			sb.WriteString("**Error**: ")
		}
		sb.WriteString(result.Content)
		sb.WriteString("\n\n")
	}

	sb.WriteString("---\n\n")
	sb.WriteString("Please provide your response based on the tool results above.\n")

	return sb.String()
}

// FallbackChatRequest converts a ChatRequest for use with non-tool-supporting models.
// It injects tool definitions into the system prompt and returns a modified request.
func FallbackChatRequest(req ChatRequest) ChatRequest {
	if len(req.Tools) == 0 {
		return req
	}

	// Inject tools into system prompt
	modifiedPrompt := InjectToolsIntoPrompt(req.SystemPrompt, req.Tools)

	return ChatRequest{
		Messages:      req.Messages,
		SystemPrompt:  modifiedPrompt,
		Tools:         nil, // Clear tools since we're using prompt injection
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		StopSequences: req.StopSequences,
	}
}

// ProcessFallbackResponse processes a response from a non-tool-supporting model.
// It parses any tool calls from the text and creates a proper ChatResponse.
func ProcessFallbackResponse(response *ChatResponse) *ChatResponse {
	// Get the text content
	textContent := response.GetText()

	// Parse tool calls from text
	toolCalls, remainingText := ParseToolCallsFromText(textContent)

	if len(toolCalls) == 0 {
		// No tool calls found, return original response
		return response
	}

	// Build new content blocks
	var newContent []ContentBlock

	// Add remaining text if any
	if remainingText != "" {
		newContent = append(newContent, ContentBlock{
			Type: ContentTypeText,
			Text: remainingText,
		})
	}

	// Add tool use blocks
	for _, tc := range toolCalls {
		newContent = append(newContent, ContentBlock{
			Type:      ContentTypeToolUse,
			ToolUseID: tc.ID,
			ToolName:  tc.Name,
			ToolInput: tc.Input,
		})
	}

	return &ChatResponse{
		Content:    newContent,
		StopReason: StopReasonToolUse,
		Usage:      response.Usage,
		Model:      response.Model,
	}
}

// FallbackProvider wraps a non-tool-supporting provider to add tool calling via prompt injection.
type FallbackProvider struct {
	wrapped Provider
}

// NewFallbackProvider creates a new fallback provider wrapper.
func NewFallbackProvider(provider Provider) *FallbackProvider {
	return &FallbackProvider{wrapped: provider}
}

// Chat implements the Provider interface with fallback tool calling support.
func (p *FallbackProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// If there are tools, use fallback mechanism
	if len(req.Tools) > 0 {
		req = FallbackChatRequest(req)
	}

	// Call the underlying provider
	response, err := p.wrapped.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	// Process response to extract tool calls from text
	return ProcessFallbackResponse(response), nil
}

// SupportsTools returns true since we provide tool support via fallback.
func (p *FallbackProvider) SupportsTools() bool {
	return true
}

// Name returns the wrapped provider's name with a fallback suffix.
func (p *FallbackProvider) Name() string {
	return p.wrapped.Name() + "_fallback"
}

// Model returns the wrapped provider's model.
func (p *FallbackProvider) Model() string {
	return p.wrapped.Model()
}
