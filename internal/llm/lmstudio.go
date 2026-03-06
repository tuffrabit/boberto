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
	InstanceID string `json:"instance_id"`
}

// lmStudioUnloadResponse represents a model unload response.
type lmStudioUnloadResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// lmStudioModel represents a model in the LM Studio models list.
type lmStudioModel struct {
	Type            string                    `json:"type"`
	Publisher       string                    `json:"publisher"`
	Key             string                    `json:"key"`
	DisplayName     string                    `json:"display_name"`
	LoadedInstances []lmStudioLoadedInstance  `json:"loaded_instances"`
}

// lmStudioLoadedInstance represents a loaded instance of a model.
type lmStudioLoadedInstance struct {
	ID     string                 `json:"id"`
	Config map[string]interface{} `json:"config"`
}

// lmStudioModelsResponse represents the response from the list models endpoint.
type lmStudioModelsResponse struct {
	Models []lmStudioModel `json:"models"`
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
// First checks if the model is already loaded, and if so, returns immediately.
// Retries on failure and returns an error if it can't recover.
func (p *LMStudioProvider) LoadModel(ctx context.Context, modelName string) error {
	// First check if the model is already loaded
	isLoaded, err := p.IsModelLoaded(ctx, modelName)
	if err != nil {
		if p.debug != nil && p.debug.IsEnabled() {
			p.debug.Section("MODEL LOAD CHECK FAILED → %s", modelName)
			p.debug.Log("Error: %v", err)
		}
		// Continue with load attempt even if check fails
	}
	
	if isLoaded {
		if p.debug != nil && p.debug.IsEnabled() {
			p.debug.Section("MODEL ALREADY LOADED → %s", modelName)
			p.debug.Log("Skipping load request - model is already in memory")
		}
		return nil
	}
	
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
// First queries the model to get its instance ID, then sends the unload request.
func (p *LMStudioProvider) unloadModelOnce(ctx context.Context, modelName string) error {
	// First, get the instance ID of the loaded model
	instanceID, err := p.getLoadedInstanceID(ctx, modelName)
	if err != nil {
		return fmt.Errorf("failed to get loaded instance ID: %w", err)
	}
	
	if instanceID == "" {
		if p.debug != nil && p.debug.IsEnabled() {
			p.debug.Log("Model %s is not loaded or has no instances", modelName)
		}
		return nil // Model is not loaded, nothing to unload
	}
	
	unloadReq := lmStudioUnloadRequest{
		InstanceID: instanceID,
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

// getLoadedInstanceID retrieves the instance ID of a loaded model.
// Returns empty string if the model is not loaded or has no instances.
func (p *LMStudioProvider) getLoadedInstanceID(ctx context.Context, modelName string) (string, error) {
	baseURL := p.lmStudioBaseURL()
	listURL := baseURL + "/api/v1/models"
	
	if p.debug != nil && p.debug.IsEnabled() {
		p.debug.Section("FETCHING INSTANCE ID → %s", modelName)
		p.debug.Log("Endpoint: GET %s", listURL)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create list models request: %w", err)
	}
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send list models request: %w", err)
	}
	defer httpResp.Body.Close()
	
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read list models response: %w", err)
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list models request failed with status %d: %s", httpResp.StatusCode, string(respBody))
	}
	
	var modelsResp lmStudioModelsResponse
	if err := json.Unmarshal(respBody, &modelsResp); err != nil {
		return "", fmt.Errorf("failed to parse list models response: %w", err)
	}
	
	// Search for the model and return its first loaded instance ID
	for _, model := range modelsResp.Models {
		// Match by key (model identifier) or display_name
		if model.Key == modelName || model.DisplayName == modelName {
			if len(model.LoadedInstances) > 0 {
				instanceID := model.LoadedInstances[0].ID
				if p.debug != nil && p.debug.IsEnabled() {
					p.debug.Log("Found instance ID: %s", instanceID)
				}
				return instanceID, nil
			}
			if p.debug != nil && p.debug.IsEnabled() {
				p.debug.Log("Model found but no loaded instances")
			}
			return "", nil // Model found but not loaded
		}
	}
	
	if p.debug != nil && p.debug.IsEnabled() {
		p.debug.Log("Model not found in available models list")
	}
	
	// Model not found in the list
	return "", nil
}

// SupportsModelManagement returns true for LM Studio.
func (p *LMStudioProvider) SupportsModelManagement() bool {
	return true
}

// GetLoadedModel returns the name of the currently loaded model in LM Studio.
// Queries the /api/v1/models endpoint and returns the first model with loaded instances.
func (p *LMStudioProvider) GetLoadedModel(ctx context.Context) (string, error) {
	if !p.SupportsModelManagement() {
		return "", nil
	}

	baseURL := p.lmStudioBaseURL()
	listURL := baseURL + "/api/v1/models"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create list models request: %w", err)
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send list models request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list models request failed with status %d", httpResp.StatusCode)
	}

	var modelsResp lmStudioModelsResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&modelsResp); err != nil {
		return "", fmt.Errorf("failed to parse list models response: %w", err)
	}

	// Return the first loaded model's key, or empty if none
	for _, model := range modelsResp.Models {
		if len(model.LoadedInstances) > 0 {
			return model.Key, nil
		}
	}
	return "", nil
}

// IsModelLoaded checks if a model is currently loaded in LM Studio.
// It queries the /api/v1/models endpoint and checks the loaded_instances field.
func (p *LMStudioProvider) IsModelLoaded(ctx context.Context, modelName string) (bool, error) {
	baseURL := p.lmStudioBaseURL()
	listURL := baseURL + "/api/v1/models"
	
	if p.debug != nil && p.debug.IsEnabled() {
		p.debug.Section("CHECKING MODEL LOAD STATUS → %s", modelName)
		p.debug.Log("Endpoint: GET %s", listURL)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create list models request: %w", err)
	}
	
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return false, fmt.Errorf("failed to send list models request: %w", err)
	}
	defer httpResp.Body.Close()
	
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read list models response: %w", err)
	}
	
	if httpResp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("list models request failed with status %d: %s", httpResp.StatusCode, string(respBody))
	}
	
	var modelsResp lmStudioModelsResponse
	if err := json.Unmarshal(respBody, &modelsResp); err != nil {
		return false, fmt.Errorf("failed to parse list models response: %w", err)
	}
	
	// Search for the model and check if it has loaded instances
	for _, model := range modelsResp.Models {
		// Match by key (model identifier) or display_name
		if model.Key == modelName || model.DisplayName == modelName {
			isLoaded := len(model.LoadedInstances) > 0
			if p.debug != nil && p.debug.IsEnabled() {
				if isLoaded {
					p.debug.Log("Found model with %d loaded instance(s)", len(model.LoadedInstances))
				} else {
					p.debug.Log("Model found but no loaded instances")
				}
			}
			return isLoaded, nil
		}
	}
	
	if p.debug != nil && p.debug.IsEnabled() {
		p.debug.Log("Model not found in available models list")
	}
	
	// Model not found in the list, consider it not loaded
	return false, nil
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
