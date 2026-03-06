// Package llm provides LLM client interfaces and implementations.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/tuffrabit/boberto/internal/debug"
)

// OpenAIProvider implements the Provider interface for OpenAI-compatible APIs.
type OpenAIProvider struct {
	apiKey string
	baseURL string
	client  *http.Client
	debug   *debug.Logger
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey, baseURL string) *OpenAIProvider {
	return NewOpenAIProviderWithDebug(apiKey, baseURL, debug.NewLogger(false))
}

// NewOpenAIProviderWithDebug creates a new OpenAI provider with debug logging.
func NewOpenAIProviderWithDebug(apiKey, baseURL string, debugLogger *debug.Logger) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{},
		debug:   debugLogger,
	}
}

// SetDebugLogger sets the debug logger for this provider.
func (p *OpenAIProvider) SetDebugLogger(debugLogger *debug.Logger) {
	p.debug = debugLogger
}

// openAIMessage represents a message in the OpenAI API format.
type openAIMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// openAIToolCall represents a tool call in the OpenAI API format.
type openAIToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

// openAIFunctionCall represents a function call in the OpenAI API format.
type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// openAITool represents a tool definition in the OpenAI API format.
type openAITool struct {
	Type     string              `json:"type"`
	Function openAIFunctionDefinition `json:"function"`
}

// openAIFunctionDefinition represents a function definition in the OpenAI API format.
type openAIFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// openAIRequest represents a request to the OpenAI API.
type openAIRequest struct {
	Model       string           `json:"model"`
	Messages    []openAIMessage  `json:"messages"`
	Tools       []openAITool     `json:"tools,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

// openAIResponse represents a response from the OpenAI API.
type openAIResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []openAIChoice   `json:"choices"`
	Usage   *openAIUsage     `json:"usage,omitempty"`
	Error   *openAIError     `json:"error,omitempty"`
}

// openAIChoice represents a choice in the OpenAI API response.
type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason,omitempty"`
}

// openAIUsage represents token usage in the OpenAI API response.
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openAIError represents an error from the OpenAI API.
type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func (e *openAIError) Error() string {
	return fmt.Sprintf("OpenAI API error: %s (type: %s, code: %s)", e.Message, e.Type, e.Code)
}

// Complete sends a completion request to the OpenAI API.
func (p *OpenAIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	// Convert messages to OpenAI format
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	
	// Add system message if provided
	if req.System != "" {
		messages = append(messages, openAIMessage{
			Role:    "system",
			Content: req.System,
		})
	}
	
	// Add conversation messages
	for _, msg := range req.Messages {
		oaiMsg := openAIMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
		if msg.Role == "tool" {
			oaiMsg.ToolCallID = msg.ToolCallID
		}
		messages = append(messages, oaiMsg)
	}
	
	// Convert tools to OpenAI format
	var tools []openAITool
	if len(req.Tools) > 0 {
		tools = make([]openAITool, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = openAITool{
				Type: "function",
				Function: openAIFunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
				},
			}
		}
	}
	
	// Set defaults for optional parameters
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096 // Default max tokens if not specified
	}
	
	oaiReq := openAIRequest{
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		MaxTokens:   maxTokens,
		Temperature: 0.7,
		Stream:      false, // Explicitly request non-streaming response
	}
	
	// Debug logging: Request
	if p.debug.IsEnabled() {
		msgMaps := make([]map[string]interface{}, len(req.Messages))
		for i, msg := range req.Messages {
			msgMaps[i] = map[string]interface{}{
				"role":         msg.Role,
				"content":      msg.Content,
				"tool_call_id": msg.ToolCallID,
			}
		}
		toolMaps := make([]map[string]interface{}, len(req.Tools))
		for i, tool := range req.Tools {
			toolMaps[i] = map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			}
		}
		p.debug.Request(req.Model, req.System, msgMaps, toolMaps)
	}
	
	body, err := json.Marshal(oaiReq)
	if err != nil {
		return Response{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("failed to create request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("failed to send request to %s: %w", p.baseURL+"/chat/completions", err)
	}
	defer httpResp.Body.Close()
	
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("failed to read response: %w", err)
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("HTTP error %d from %s: %s", httpResp.StatusCode, p.baseURL+"/chat/completions", string(respBody))
	}
	
	var oaiResp openAIResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return Response{}, fmt.Errorf("failed to parse response: %w", err)
	}
	
	if oaiResp.Error != nil {
		return Response{}, oaiResp.Error
	}
	
	if len(oaiResp.Choices) == 0 {
		return Response{}, fmt.Errorf("no choices in response")
	}
	
	choice := oaiResp.Choices[0]
	message := choice.Message
	
	// Convert tool calls
	var toolCalls []ToolCall
	if len(message.ToolCalls) > 0 {
		toolCalls = make([]ToolCall, len(message.ToolCalls))
		for i, tc := range message.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				// If arguments can't be parsed as JSON, store them as a string
				args = map[string]any{"raw": tc.Function.Arguments}
			}
			toolCalls[i] = ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			}
		}
	}
	
	// Determine if we're done based on finish_reason
	done := choice.FinishReason == "stop"
	
	// Handle optional usage field
	var usage TokenUsage
	if oaiResp.Usage != nil {
		usage = TokenUsage{
			InputTokens:  oaiResp.Usage.PromptTokens,
			OutputTokens: oaiResp.Usage.CompletionTokens,
			TotalTokens:  oaiResp.Usage.TotalTokens,
		}
	}
	
	resp := Response{
		Content:   message.Content,
		ToolCalls: toolCalls,
		Usage:     usage,
		Done:      done,
	}
	
	// Debug logging: Response
	if p.debug.IsEnabled() {
		tcMaps := make([]map[string]interface{}, len(toolCalls))
		for i, tc := range toolCalls {
			tcMaps[i] = map[string]interface{}{
				"id":        tc.ID,
				"name":      tc.Name,
				"arguments": tc.Arguments,
			}
		}
		p.debug.Response(resp.Content, tcMaps, map[string]int{
			"input":  resp.Usage.InputTokens,
			"output": resp.Usage.OutputTokens,
			"total":  resp.Usage.TotalTokens,
		})
	}
	
	return resp, nil
}

// CountTokens estimates token count using the approximate method.
// OpenAI doesn't provide a local tokenizer, so we use the fallback.
func (p *OpenAIProvider) CountTokens(text string) (int, error) {
	// Approximate: 4 characters ≈ 1 token
	return approximateTokenCount(text), nil
}

// LoadModel is not supported for cloud OpenAI API.
func (p *OpenAIProvider) LoadModel(ctx context.Context, modelName string) error {
	return fmt.Errorf("OpenAI provider does not support model management")
}

// UnloadModel is not supported for cloud OpenAI API.
func (p *OpenAIProvider) UnloadModel(ctx context.Context, modelName string) error {
	return fmt.Errorf("OpenAI provider does not support model management")
}

// SupportsModelManagement returns false for the OpenAI cloud API.
func (p *OpenAIProvider) SupportsModelManagement() bool {
	return false
}


