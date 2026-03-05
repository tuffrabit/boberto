// Package llm provides LLM client interfaces and implementations.
package llm

import (
	"context"
	"fmt"
	"time"
)

// Message represents a chat message.
type Message struct {
	Role    string // "system", "user", "assistant", "tool"
	Content string
	// ToolCallID is set when Role is "tool" (the response to a tool call)
	ToolCallID string
}

// ToolCall represents a tool call from the LLM.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any // Parsed JSON arguments
}

// ToolDefinition defines a tool available to the LLM.
// The Parameters field should be a JSON Schema object.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema for the tool parameters
}

// Request represents a completion request.
type Request struct {
	Model     string
	System    string          // System prompt (for providers that support it separately)
	Messages  []Message       // Conversation history
	Tools     []ToolDefinition // Available tools
	MaxTokens int             // Maximum tokens for the response
}

// Response represents a completion response.
type Response struct {
	Content   string     // Text content from the LLM
	ToolCalls []ToolCall // Tool calls requested by the LLM
	Usage     TokenUsage // Token usage information
	Done      bool       // Whether the conversation should end
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Provider is the interface for LLM providers.
type Provider interface {
	// Complete sends a request to the LLM and returns the response.
	Complete(ctx context.Context, req Request) (Response, error)
	
	// CountTokens estimates the number of tokens in the given text.
	// Returns an error if the tokenizer fails.
	CountTokens(text string) (int, error)
	
	// LoadModel loads a model into memory (for local providers).
	// Retries on failure and returns an error if it can't recover.
	LoadModel(ctx context.Context, modelName string) error
	
	// UnloadModel unloads a model from memory (for local providers).
	// Retries on failure and returns an error if it can't recover.
	UnloadModel(ctx context.Context, modelName string) error
	
	// SupportsModelManagement returns true if this provider supports
	// LoadModel/UnloadModel operations.
	SupportsModelManagement() bool
}

// retryOperation retries an operation up to maxRetries times with the given delay.
func retryOperation(ctx context.Context, maxRetries int, delay time.Duration, operation func() error) error {
	var lastErr error
	
	for i := 0; i <= maxRetries; i++ {
		if err := operation(); err != nil {
			lastErr = err
			if i < maxRetries {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
					continue
				}
			}
		} else {
			return nil
		}
	}
	
	return fmt.Errorf("operation failed after %d retries: %w", maxRetries, lastErr)
}

// defaultRetryDelay is the delay between retry attempts.
const defaultRetryDelay = 2 * time.Second

// defaultMaxRetries is the default number of retries for model operations.
const defaultMaxRetries = 3
