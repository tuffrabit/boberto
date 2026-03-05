// Package tools provides the tool system for agent operations.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tuffrabit/boberto/internal/config"
)

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the unique name of the tool.
	Name() string

	// Description returns a description of what the tool does.
	Description() string

	// Parameters returns the JSON Schema for the tool's parameters.
	// This describes what arguments the LLM should provide when calling the tool.
	Parameters() map[string]any

	// Execute runs the tool with the given arguments.
	// The whitelist is passed for sensitive tools to validate permissions.
	Execute(ctx context.Context, args map[string]any, whitelist config.Whitelist) (Result, error)

	// IsSensitive returns true if this tool requires whitelist checking.
	// Sensitive tools (bash, web_fetch) must validate against whitelist in Execute.
	IsSensitive() bool
}

// Result represents the outcome of a tool execution.
type Result struct {
	// Content is the main output of the tool.
	Content string `json:"content"`

	// Error is set if the tool execution failed.
	// This is separate from the error return value which indicates
	// system-level errors (like invalid arguments).
	Error string `json:"error,omitempty"`

	// IsError indicates whether this result represents an error.
	IsError bool `json:"is_error"`
}

// ToJSON serializes the result to JSON for returning to the LLM.
func (r Result) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// String returns a string representation of the result.
func (r Result) String() string {
	if r.IsError {
		return fmt.Sprintf("Error: %s", r.Error)
	}
	return r.Content
}

// Success creates a successful result with the given content.
func Success(content string) Result {
	return Result{
		Content: content,
		IsError: false,
	}
}

// Failure creates an error result with the given error message.
func Failure(errMsg string) Result {
	return Result{
		Error:   errMsg,
		IsError: true,
	}
}

// ToolDefinition converts a Tool to an LLM ToolDefinition.
// This is used when registering tools with the LLM client.
func ToolDefinition(t Tool) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  t.Parameters(),
		},
	}
}

// ExtractString extracts a string parameter from the arguments map.
// Returns an error if the parameter is missing or not a string.
func ExtractString(args map[string]any, name string) (string, error) {
	val, ok := args[name]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", name)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", name)
	}
	return str, nil
}

// ExtractInt extracts an integer parameter from the arguments map.
// Returns an error if the parameter is missing or not a number.
func ExtractInt(args map[string]any, name string) (int, error) {
	val, ok := args[name]
	if !ok {
		return 0, fmt.Errorf("missing required parameter: %s", name)
	}

	switch v := val.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("parameter %s must be a number", name)
	}
}

// ExtractBool extracts a boolean parameter from the arguments map.
// Returns the value and true if present, false and false if missing.
func ExtractBool(args map[string]any, name string) (bool, bool) {
	val, ok := args[name]
	if !ok {
		return false, false
	}
	b, ok := val.(bool)
	return b, ok
}

// OptionalString extracts an optional string parameter from the arguments map.
// Returns the value and true if present, empty string and false if missing.
func OptionalString(args map[string]any, name string) (string, bool) {
	val, ok := args[name]
	if !ok {
		return "", false
	}
	str, ok := val.(string)
	return str, ok
}

// OptionalInt extracts an optional integer parameter from the arguments map.
// Returns the value and true if present, 0 and false if missing or invalid type.
func OptionalInt(args map[string]any, name string) (int, bool) {
	val, ok := args[name]
	if !ok {
		return 0, false
	}

	switch v := val.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}
