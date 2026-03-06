// Package llm provides LLM client interfaces and implementations.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/tuffrabit/boberto/internal/debug"
)

// LMStudioProvider implements the Provider interface for LM Studio.
// It uses OpenAI-compatible API for chat and LM Studio's native API for model management.
type LMStudioProvider struct {
	baseURL string
	client  *http.Client
	openAI  *OpenAIProvider // Reuse OpenAI provider for chat completions
	debug   *debug.Logger
}

// NewLMStudioProvider creates a new LM Studio provider.
func NewLMStudioProvider(baseURL string) *LMStudioProvider {
	if baseURL == "" {
		baseURL = "http://localhost:1234/v1"
	}
	return &LMStudioProvider{
		baseURL: baseURL,
		client:  &http.Client{},
		openAI:  NewOpenAIProvider("", baseURL),
	}
}

// lmStudioLoadRequest represents a model load request.
type lmStudioLoadRequest struct {
	Model string `json:"model"`
}

// lmStudioLoadResponse represents a model load response.
type lmStudioLoadResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// lmStudioUnloadRequest represents a model unload request.
type lmStudioUnloadRequest struct {
	Model string `json:"model"`
}

// lmStudioUnloadResponse represents a model unload response.
type lmStudioUnloadResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// Complete delegates to the OpenAI-compatible provider.
func (p *LMStudioProvider) Complete(ctx context.Context, req Request) (Response, error) {
	return p.openAI.Complete(ctx, req)
}

// CountTokens delegates to the OpenAI-compatible provider.
func (p *LMStudioProvider) CountTokens(text string) (int, error) {
	return p.openAI.CountTokens(text)
}

// LoadModel loads a model into LM Studio memory.
// Retries on failure and returns an error if it can't recover.
func (p *LMStudioProvider) LoadModel(ctx context.Context, modelName string) error {
	return retryOperation(ctx, defaultMaxRetries, defaultRetryDelay, func() error {
		return p.loadModelOnce(ctx, modelName)
	})
}

// loadModelOnce attempts to load a model once.
func (p *LMStudioProvider) loadModelOnce(ctx context.Context, modelName string) error {
	loadReq := lmStudioLoadRequest{
		Model: modelName,
	}
	
	body, err := json.Marshal(loadReq)
	if err != nil {
		return fmt.Errorf("failed to marshal load request: %w", err)
	}
	
	baseURL := p.lmStudioBaseURL()
	loadURL := baseURL + "/api/v1/models/load"
	
	if p.debug != nil && p.debug.IsEnabled() {
		p.debug.Section("MODEL LOAD REQUEST → %s", modelName)
		p.debug.Log("Endpoint: POST %s", loadURL)
		p.debug.Log("Request body: %s", string(body))
	}
	
	// LM Studio load endpoint is at /api/v1/models/load
	httpReq, err := http.NewRequestWithContext(ctx, "POST", loadURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create load request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send load request: %w", err)
	}
	defer httpResp.Body.Close()
	
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read load response: %w", err)
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("load request failed with status %d: %s", httpResp.StatusCode, string(respBody))
	}
	
	var loadResp lmStudioLoadResponse
	if err := json.Unmarshal(respBody, &loadResp); err != nil {
		// If we can't parse the response, check if it contains "loaded" or success indicators
		if httpResp.StatusCode == http.StatusOK {
			return nil
		}
		return fmt.Errorf("failed to parse load response: %w", err)
	}
	
	// Check if the load was successful
	if loadResp.Status != "loaded" && loadResp.Status != "success" && loadResp.Status != "ok" {
		return fmt.Errorf("model load failed: %s", loadResp.Message)
	}
	
	return nil
}

// UnloadModel unloads a model from LM Studio memory.
// Retries on failure and returns an error if it can't recover.
func (p *LMStudioProvider) UnloadModel(ctx context.Context, modelName string) error {
	return retryOperation(ctx, defaultMaxRetries, defaultRetryDelay, func() error {
		return p.unloadModelOnce(ctx, modelName)
	})
}

// unloadModelOnce attempts to unload a model once.
func (p *LMStudioProvider) unloadModelOnce(ctx context.Context, modelName string) error {
	unloadReq := lmStudioUnloadRequest{
		Model: modelName,
	}
	
	body, err := json.Marshal(unloadReq)
	if err != nil {
		return fmt.Errorf("failed to marshal unload request: %w", err)
	}
	
	baseURL := p.lmStudioBaseURL()
	unloadURL := baseURL + "/api/v1/models/unload"
	
	if p.debug != nil && p.debug.IsEnabled() {
		p.debug.Section("MODEL UNLOAD REQUEST → %s", modelName)
		p.debug.Log("Endpoint: POST %s", unloadURL)
		p.debug.Log("Request body: %s", string(body))
	}
	
	// LM Studio unload endpoint is at /api/v1/models/unload
	httpReq, err := http.NewRequestWithContext(ctx, "POST", unloadURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create unload request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send unload request: %w", err)
	}
	defer httpResp.Body.Close()
	
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read unload response: %w", err)
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("unload request failed with status %d: %s", httpResp.StatusCode, string(respBody))
	}
	
	var unloadResp lmStudioUnloadResponse
	if err := json.Unmarshal(respBody, &unloadResp); err != nil {
		// If we can't parse the response but got OK status, consider it success
		if httpResp.StatusCode == http.StatusOK {
			return nil
		}
		return fmt.Errorf("failed to parse unload response: %w", err)
	}
	
	// Check if the unload was successful
	if unloadResp.Status != "unloaded" && unloadResp.Status != "success" && unloadResp.Status != "ok" {
		return fmt.Errorf("model unload failed: %s", unloadResp.Message)
	}
	
	return nil
}

// SupportsModelManagement returns true for LM Studio.
func (p *LMStudioProvider) SupportsModelManagement() bool {
	return true
}

// SetDebugLogger sets the debug logger for this provider.
func (p *LMStudioProvider) SetDebugLogger(debugLogger *debug.Logger) {
	p.debug = debugLogger
	p.openAI.SetDebugLogger(debugLogger)
}

// lmStudioBaseURL extracts the LM Studio base URL (without /v1) from the configured baseURL.
// The baseURL is typically "http://host:port/v1" for OpenAI compatibility,
// but LM Studio's native API is at "http://host:port/api/v1".
func (p *LMStudioProvider) lmStudioBaseURL() string {
	// Parse the configured baseURL
	u, err := url.Parse(p.baseURL)
	if err != nil {
		// Fallback to localhost if parsing fails
		return "http://localhost:1234"
	}
	
	// Build the base URL without the path (removing /v1 suffix)
	base := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	return base
}
