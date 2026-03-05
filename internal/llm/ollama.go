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

// OllamaProvider implements the Provider interface for Ollama.
// It uses OpenAI-compatible API for chat and Ollama's native API for model management.
type OllamaProvider struct {
	baseURL string
	client  *http.Client
	openAI  *OpenAIProvider // Reuse OpenAI provider for chat completions
}

// NewOllamaProvider creates a new Ollama provider.
func NewOllamaProvider(baseURL string) *OllamaProvider {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	// Ollama's OpenAI-compatible endpoint is at /v1
	return &OllamaProvider{
		baseURL: baseURL,
		client:  &http.Client{},
		openAI:  NewOpenAIProvider("", baseURL+"/v1"),
	}
}

// ollamaGenerateRequest represents a generate request for model management.
type ollamaGenerateRequest struct {
	Model    string `json:"model"`
	Prompt   string `json:"prompt,omitempty"`
	KeepAlive string `json:"keep_alive,omitempty"` // Duration string or "0" to unload
}

// ollamaGenerateResponse represents a generate response.
type ollamaGenerateResponse struct {
	Model     string `json:"model"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
}

// Complete delegates to the OpenAI-compatible provider.
func (p *OllamaProvider) Complete(ctx context.Context, req Request) (Response, error) {
	return p.openAI.Complete(ctx, req)
}

// CountTokens delegates to the OpenAI-compatible provider.
func (p *OllamaProvider) CountTokens(text string) (int, error) {
	return p.openAI.CountTokens(text)
}

// LoadModel loads a model into Ollama memory.
// Uses the generate API with keep_alive to keep the model loaded.
// Retries on failure and returns an error if it can't recover.
func (p *OllamaProvider) LoadModel(ctx context.Context, modelName string) error {
	return retryOperation(ctx, defaultMaxRetries, defaultRetryDelay, func() error {
		return p.loadModelOnce(ctx, modelName)
	})
}

// loadModelOnce attempts to load a model once.
func (p *OllamaProvider) loadModelOnce(ctx context.Context, modelName string) error {
	// Use keep_alive: -1 to keep the model loaded indefinitely
	genReq := ollamaGenerateRequest{
		Model:     modelName,
		Prompt:    "", // Empty prompt to just load the model
		KeepAlive: "-1",
	}
	
	body, err := json.Marshal(genReq)
	if err != nil {
		return fmt.Errorf("failed to marshal load request: %w", err)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create load request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send load request: %w", err)
	}
	defer httpResp.Body.Close()
	
	// Read and process the streaming response
	decoder := json.NewDecoder(httpResp.Body)
	var lastResponse ollamaGenerateResponse
	
	for {
		var resp ollamaGenerateResponse
		if err := decoder.Decode(&resp); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode load response: %w", err)
		}
		lastResponse = resp
		
		if resp.Error != "" {
			return fmt.Errorf("model load error: %s", resp.Error)
		}
		
		if resp.Done {
			break
		}
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("load request failed with status %d", httpResp.StatusCode)
	}
	
	// Check if there was an error in the response
	if lastResponse.Error != "" {
		return fmt.Errorf("model load failed: %s", lastResponse.Error)
	}
	
	return nil
}

// UnloadModel unloads a model from Ollama memory.
// Uses the generate API with keep_alive: 0 to unload the model immediately.
// Retries on failure and returns an error if it can't recover.
func (p *OllamaProvider) UnloadModel(ctx context.Context, modelName string) error {
	return retryOperation(ctx, defaultMaxRetries, defaultRetryDelay, func() error {
		return p.unloadModelOnce(ctx, modelName)
	})
}

// unloadModelOnce attempts to unload a model once.
func (p *OllamaProvider) unloadModelOnce(ctx context.Context, modelName string) error {
	// Use keep_alive: 0 to unload the model immediately
	genReq := ollamaGenerateRequest{
		Model:     modelName,
		Prompt:    "", // Empty prompt
		KeepAlive: "0",
	}
	
	body, err := json.Marshal(genReq)
	if err != nil {
		return fmt.Errorf("failed to marshal unload request: %w", err)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create unload request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send unload request: %w", err)
	}
	defer httpResp.Body.Close()
	
	// Read and process the streaming response
	decoder := json.NewDecoder(httpResp.Body)
	var lastResponse ollamaGenerateResponse
	
	for {
		var resp ollamaGenerateResponse
		if err := decoder.Decode(&resp); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode unload response: %w", err)
		}
		lastResponse = resp
		
		if resp.Error != "" {
			return fmt.Errorf("model unload error: %s", resp.Error)
		}
		
		if resp.Done {
			break
		}
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("unload request failed with status %d", httpResp.StatusCode)
	}
	
	// Check if there was an error in the response
	if lastResponse.Error != "" {
		return fmt.Errorf("model unload failed: %s", lastResponse.Error)
	}
	
	return nil
}

// SupportsModelManagement returns true for Ollama.
func (p *OllamaProvider) SupportsModelManagement() bool {
	return true
}

// SetDebugLogger sets the debug logger for this provider.
func (p *OllamaProvider) SetDebugLogger(debugLogger *debug.Logger) {
	p.openAI.SetDebugLogger(debugLogger)
}
