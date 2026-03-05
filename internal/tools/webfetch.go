// Package tools provides the tool system for agent operations.
package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tuffrabit/boberto/internal/config"
)

// WebFetchTool fetches content from URLs (sensitive tool).
type WebFetchTool struct {
	timeout    time.Duration
	maxSize    int64
	httpClient *http.Client
}

// NewWebFetchTool creates a new web_fetch tool.
func NewWebFetchTool(timeout time.Duration) *WebFetchTool {
	if timeout <= 0 {
		timeout = 30 * time.Second // Default 30 second timeout
	}
	return &WebFetchTool{
		timeout: timeout,
		maxSize: 10 * 1024 * 1024, // 10MB max
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Name returns the tool name.
func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

// Description returns the tool description.
func (t *WebFetchTool) Description() string {
	return "Fetch content from a URL. URLs must match patterns in the whitelist."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *WebFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL to fetch",
			},
			"max_length": map[string]any{
				"type":        "integer",
				"description": "Maximum characters to return (default: 10000)",
			},
		},
		"required": []string{"url"},
	}
}

// Execute runs the web_fetch tool.
// CRITICAL: This tool validates the URL against the whitelist before fetching.
func (t *WebFetchTool) Execute(ctx context.Context, args map[string]any, whitelist config.Whitelist) (Result, error) {
	url, err := ExtractString(args, "url")
	if err != nil {
		return Failure(err.Error()), err
	}

	// Validate URL against whitelist
	if !whitelist.AllowsWebFetch(url) {
		return Failure(fmt.Sprintf("URL not in whitelist: %s", url)), fmt.Errorf("URL not in whitelist")
	}

	// Get max length
	maxLength := 10000
	if maxVal, ok := OptionalInt(args, "max_length"); ok && maxVal > 0 {
		maxLength = maxVal
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Failure(fmt.Sprintf("failed to create request: %v", err)), nil
	}

	// Set a user agent
	req.Header.Set("User-Agent", "Boberto-Agent/1.0")

	// Execute request
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return Failure(fmt.Sprintf("failed to fetch URL: %v", err)), nil
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return Failure(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)), nil
	}

	// Read response body with size limit
	reader := io.LimitReader(resp.Body, t.maxSize)
	content, err := io.ReadAll(reader)
	if err != nil {
		return Failure(fmt.Sprintf("failed to read response: %v", err)), nil
	}

	// Convert to string and truncate if needed
	result := string(content)
	if len(result) > maxLength {
		result = result[:maxLength] + fmt.Sprintf("\n\n[Content truncated: %d bytes total, showing first %d]", len(content), maxLength)
	}

	// Add metadata
	var output strings.Builder
	output.WriteString(fmt.Sprintf("URL: %s\n", url))
	output.WriteString(fmt.Sprintf("Status: %d\n", resp.StatusCode))
	output.WriteString(fmt.Sprintf("Content-Type: %s\n", resp.Header.Get("Content-Type")))
	output.WriteString(fmt.Sprintf("Content-Length: %d bytes\n\n", len(content)))
	output.WriteString(result)

	return Success(output.String()), nil
}

// IsSensitive returns true - web_fetch is a sensitive tool requiring whitelist.
func (t *WebFetchTool) IsSensitive() bool {
	return true
}
