// Package llm provides LLM client interfaces and implementations.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tuffrabit/boberto/internal/debug"
)

// AnthropicProvider implements the Provider interface for Anthropic's Claude API.
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	debug   *debug.Logger
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey, baseURL string) *AnthropicProvider {
	return NewAnthropicProviderWithDebug(apiKey, baseURL, debug.NewLogger(false))
}

// NewAnthropicProviderWithDebug creates a new Anthropic provider with debug logging.
func NewAnthropicProviderWithDebug(apiKey, baseURL string, debugLogger *debug.Logger) *AnthropicProvider {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return &AnthropicProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{},
		debug:   debugLogger,
	}
}

// SetDebugLogger sets the debug logger for this provider.
func (p *AnthropicProvider) SetDebugLogger(debugLogger *debug.Logger) {
	p.debug = debugLogger
}

// anthropicMessage represents a message in the Anthropic API format.
type anthropicMessage struct {
	Role    string                      `json:"role"`
	Content []anthropicContentBlock     `json:"content"`
}

// anthropicContentBlock represents a content block in the Anthropic API.
type anthropicContentBlock struct {
	Type string `json:"type"`
	
	// For text content
	Text string `json:"text,omitempty"`
	
	// For tool use
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
	
	// For tool result
	ToolUseID string                   `json:"tool_use_id,omitempty"`
	Content   []anthropicContentBlock  `json:"content,omitempty"`
	IsError   bool                     `json:"is_error,omitempty"`
}

// anthropicTool represents a tool definition in the Anthropic API format.
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// anthropicRequest represents a request to the Anthropic API.
type anthropicRequest struct {
	Model     string                `json:"model"`
	MaxTokens int                   `json:"max_tokens"`
	System    string                `json:"system,omitempty"`
	Messages  []anthropicMessage    `json:"messages"`
	Tools     []anthropicTool       `json:"tools,omitempty"`
}

// anthropicResponse represents a response from the Anthropic API.
type anthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []anthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence string                  `json:"stop_sequence,omitempty"`
	Usage        anthropicUsage          `json:"usage"`
	Error        *anthropicError         `json:"error,omitempty"`
}

// anthropicUsage represents token usage in the Anthropic API response.
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicError represents an error from the Anthropic API.
type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e *anthropicError) Error() string {
	return fmt.Sprintf("Anthropic API error (%s): %s", e.Type, e.Message)
}

// Complete sends a completion request to the Anthropic API.
func (p *AnthropicProvider) Complete(ctx context.Context, req Request) (Response, error) {
	// Convert messages to Anthropic format
	messages := make([]anthropicMessage, 0, len(req.Messages))
	
	for _, msg := range req.Messages {
		var content []anthropicContentBlock
		
		switch msg.Role {
		case "user":
			content = []anthropicContentBlock{{
				Type: "text",
				Text: msg.Content,
			}}
		case "assistant":
			// Assistant messages might contain text or tool_use
			content = []anthropicContentBlock{{
				Type: "text",
				Text: msg.Content,
			}}
		case "tool":
			// Tool results are sent as user messages with tool_result content
			content = []anthropicContentBlock{{
				Type: "tool_result",
				ToolUseID: msg.ToolCallID,
				Content: []anthropicContentBlock{{
					Type: "text",
					Text: msg.Content,
				}},
			}}
			messages = append(messages, anthropicMessage{
				Role:    "user",
				Content: content,
			})
			continue
		default:
			content = []anthropicContentBlock{{
				Type: "text",
				Text: msg.Content,
			}}
		}
		
		messages = append(messages, anthropicMessage{
			Role:    msg.Role,
			Content: content,
		})
	}
	
	// Convert tools to Anthropic format
	var tools []anthropicTool
	if len(req.Tools) > 0 {
		tools = make([]anthropicTool, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = anthropicTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.Parameters,
			}
		}
	}
	
	anthReq := anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  messages,
		Tools:     tools,
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
	
	body, err := json.Marshal(anthReq)
	if err != nil {
		return Response{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("failed to create request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", p.apiKey)
	httpReq.Header.Set("Anthropic-Version", "2023-06-01")
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer httpResp.Body.Close()
	
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("failed to read response: %w", err)
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("HTTP error %d: %s", httpResp.StatusCode, string(respBody))
	}
	
	var anthResp anthropicResponse
	if err := json.Unmarshal(respBody, &anthResp); err != nil {
		return Response{}, fmt.Errorf("failed to parse response: %w", err)
	}
	
	if anthResp.Error != nil {
		return Response{}, anthResp.Error
	}
	
	// Parse content blocks
	var content strings.Builder
	var toolCalls []ToolCall
	
	for _, block := range anthResp.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}
	
	// Determine if we're done based on stop_reason
	done := anthResp.StopReason == "end_turn"
	
	resp := Response{
		Content:   content.String(),
		ToolCalls: toolCalls,
		Usage: TokenUsage{
			InputTokens:  anthResp.Usage.InputTokens,
			OutputTokens: anthResp.Usage.OutputTokens,
			TotalTokens:  anthResp.Usage.InputTokens + anthResp.Usage.OutputTokens,
		},
		Done: done,
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
// Anthropic provides a tokenizer but it requires external dependencies.
func (p *AnthropicProvider) CountTokens(text string) (int, error) {
	// Approximate: 4 characters ≈ 1 token
	return approximateTokenCount(text), nil
}

// LoadModel is not supported for cloud Anthropic API.
func (p *AnthropicProvider) LoadModel(ctx context.Context, modelName string) error {
	return fmt.Errorf("Anthropic provider does not support model management")
}

// UnloadModel is not supported for cloud Anthropic API.
func (p *AnthropicProvider) UnloadModel(ctx context.Context, modelName string) error {
	return fmt.Errorf("Anthropic provider does not support model management")
}

// SupportsModelManagement returns false for the Anthropic cloud API.
func (p *AnthropicProvider) SupportsModelManagement() bool {
	return false
}
