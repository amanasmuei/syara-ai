// Package tools provides the tool registry and base tool interface for the AI agent.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Tool defines the interface that all agent tools must implement.
type Tool interface {
	// Name returns the unique identifier for the tool.
	Name() string

	// Description returns a human-readable description of what the tool does.
	// This is used by the LLM to understand when to use the tool.
	Description() string

	// InputSchema returns the JSON schema for the tool's input parameters.
	InputSchema() map[string]interface{}

	// Execute runs the tool with the given input and returns the result.
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolDefinition represents a tool definition for the Anthropic API.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ToolCall represents a tool call request from the LLM.
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

// ToolExecution tracks the execution of a tool for logging/metrics.
type ToolExecution struct {
	ToolName    string        `json:"tool_name"`
	Input       string        `json:"input"`
	Output      string        `json:"output"`
	Duration    time.Duration `json:"duration_ms"`
	Error       string        `json:"error,omitempty"`
	ExecutedAt  time.Time     `json:"executed_at"`
	Success     bool          `json:"success"`
}

// Registry manages and executes agent tools.
type Registry struct {
	tools      map[string]Tool
	logger     *slog.Logger
	executions []ToolExecution
	mu         sync.RWMutex
	execMu     sync.Mutex
}

// NewRegistry creates a new tool registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}

	return &Registry{
		tools:      make(map[string]Tool),
		logger:     logger.With("component", "tool_registry"),
		executions: make([]ToolExecution, 0),
	}
}

// Register adds a tool to the registry.
// Returns an error if a tool with the same name is already registered.
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}

	r.tools[name] = tool
	r.logger.Info("tool registered",
		"name", name,
		"description", tool.Description(),
	)

	return nil
}

// MustRegister registers a tool and panics on error.
// Use this for tools that must be registered at startup.
func (r *Registry) MustRegister(tool Tool) {
	if err := r.Register(tool); err != nil {
		panic(err)
	}
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return fmt.Errorf("tool not found: %s", name)
	}

	delete(r.tools, name)
	r.logger.Info("tool unregistered", "name", name)

	return nil
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.tools[name]
	return tool, ok
}

// List returns all registered tool names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// GetToolDefinitions returns all tool definitions in Anthropic API format.
func (r *Registry) GetToolDefinitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.InputSchema(),
		})
	}
	return defs
}

// Execute runs a tool by name with the given input.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()

	if !ok {
		err := fmt.Errorf("unknown tool: %s", name)
		r.logger.Error("tool execution failed", "name", name, "error", err)
		return "", err
	}

	start := time.Now()
	r.logger.Debug("executing tool",
		"name", name,
		"input_size", len(input),
	)

	result, err := tool.Execute(ctx, input)
	duration := time.Since(start)

	// Track execution
	execution := ToolExecution{
		ToolName:   name,
		Input:      string(input),
		Output:     result,
		Duration:   duration,
		ExecutedAt: start,
		Success:    err == nil,
	}
	if err != nil {
		execution.Error = err.Error()
	}
	r.trackExecution(execution)

	if err != nil {
		r.logger.Error("tool execution failed",
			"name", name,
			"duration_ms", duration.Milliseconds(),
			"error", err,
		)
		return "", fmt.Errorf("tool %s execution failed: %w", name, err)
	}

	r.logger.Info("tool execution completed",
		"name", name,
		"duration_ms", duration.Milliseconds(),
		"output_size", len(result),
	)

	return result, nil
}

// ExecuteToolCall executes a ToolCall and returns a ToolResult.
func (r *Registry) ExecuteToolCall(ctx context.Context, call ToolCall) ToolResult {
	result, err := r.Execute(ctx, call.Name, call.Input)

	toolResult := ToolResult{
		ToolUseID: call.ID,
		Content:   result,
	}

	if err != nil {
		toolResult.Content = fmt.Sprintf("Error: %v", err)
		toolResult.IsError = true
	}

	return toolResult
}

// ExecuteToolCalls executes multiple tool calls and returns their results.
func (r *Registry) ExecuteToolCalls(ctx context.Context, calls []ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))

	for i, call := range calls {
		results[i] = r.ExecuteToolCall(ctx, call)
	}

	return results
}

// trackExecution records a tool execution for metrics.
func (r *Registry) trackExecution(exec ToolExecution) {
	r.execMu.Lock()
	defer r.execMu.Unlock()

	// Keep only last 1000 executions
	if len(r.executions) >= 1000 {
		r.executions = r.executions[1:]
	}
	r.executions = append(r.executions, exec)
}

// GetRecentExecutions returns the most recent tool executions.
func (r *Registry) GetRecentExecutions(limit int) []ToolExecution {
	r.execMu.Lock()
	defer r.execMu.Unlock()

	if limit <= 0 || limit > len(r.executions) {
		limit = len(r.executions)
	}

	// Return in reverse order (most recent first)
	result := make([]ToolExecution, limit)
	for i := 0; i < limit; i++ {
		result[i] = r.executions[len(r.executions)-1-i]
	}
	return result
}

// GetExecutionStats returns statistics about tool executions.
func (r *Registry) GetExecutionStats() map[string]ToolStats {
	r.execMu.Lock()
	defer r.execMu.Unlock()

	stats := make(map[string]ToolStats)

	for _, exec := range r.executions {
		s, ok := stats[exec.ToolName]
		if !ok {
			s = ToolStats{ToolName: exec.ToolName}
		}

		s.TotalCalls++
		s.TotalDuration += exec.Duration
		if exec.Success {
			s.SuccessCount++
		} else {
			s.ErrorCount++
		}

		stats[exec.ToolName] = s
	}

	// Calculate averages
	for name, s := range stats {
		if s.TotalCalls > 0 {
			s.AvgDuration = s.TotalDuration / time.Duration(s.TotalCalls)
			s.SuccessRate = float64(s.SuccessCount) / float64(s.TotalCalls)
		}
		stats[name] = s
	}

	return stats
}

// ToolStats holds statistics for a tool.
type ToolStats struct {
	ToolName      string        `json:"tool_name"`
	TotalCalls    int           `json:"total_calls"`
	SuccessCount  int           `json:"success_count"`
	ErrorCount    int           `json:"error_count"`
	TotalDuration time.Duration `json:"total_duration_ms"`
	AvgDuration   time.Duration `json:"avg_duration_ms"`
	SuccessRate   float64       `json:"success_rate"`
}

// ClearExecutions clears all execution history.
func (r *Registry) ClearExecutions() {
	r.execMu.Lock()
	defer r.execMu.Unlock()
	r.executions = make([]ToolExecution, 0)
}
