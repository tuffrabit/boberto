// Package llm provides LLM client interfaces and implementations.
package llm

import (
	"fmt"
	"strings"

	"github.com/tuffrabit/boberto/internal/config"
)

// NewProviderFromConfig creates a Provider instance based on the model configuration.
// It examines the APIType and Provider fields to determine which implementation to use.
func NewProviderFromConfig(modelConfig config.ModelConfig) (Provider, error) {
	apiType := strings.ToLower(modelConfig.APIType)
	provider := strings.ToLower(modelConfig.Provider)

	switch apiType {
	case "openai":
		// Check for provider-specific implementations
		switch provider {
		case "lmstudio":
			// LM Studio uses OpenAI-compatible API at /v1
			// Extract base URL from the full URI (e.g., http://localhost:1234/v1/chat/completions -> http://localhost:1234/v1)
			baseURL := extractBaseURL(modelConfig.URI, "/v1")
			return NewLMStudioProvider(baseURL), nil
		case "ollama":
			// For Ollama, extract the base URL from the URI
			// URI is typically http://localhost:11434/v1/chat/completions
			// Ollama's native API is at /api, but we use OpenAI-compatible at /v1
			baseURL := extractBaseURL(modelConfig.URI, "")
			return NewOllamaProvider(baseURL), nil
		default:
			// Standard OpenAI-compatible provider
			// The URI contains the full endpoint, but OpenAI provider expects base URL
			baseURL := extractBaseURL(modelConfig.URI, "/v1")
			return NewOpenAIProvider(modelConfig.APIKey, baseURL), nil
		}

	case "anthropic":
		// Anthropic provider expects base URL, URI contains full endpoint
		baseURL := extractBaseURL(modelConfig.URI, "")
		return NewAnthropicProvider(modelConfig.APIKey, baseURL), nil

	default:
		return nil, fmt.Errorf("unsupported API type: %s", modelConfig.APIType)
	}
}

// extractBaseURL extracts the base URL from a full API endpoint URL.
// For example, http://localhost:1234/v1/chat/completions with suffix /v1 -> http://localhost:1234/v1
// http://localhost:11434/v1/chat/completions with suffix "" -> http://localhost:11434
func extractBaseURL(uri string, suffix string) string {
	// Find the path portion after the host
	if idx := strings.Index(uri, "://"); idx != -1 {
		// Skip past "://"
		start := idx + 3
		// Find the next slash after the host:port
		hostEnd := strings.Index(uri[start:], "/")
		if hostEnd != -1 {
			base := uri[:start+hostEnd]
			if suffix != "" {
				return base + suffix
			}
			return base
		}
	}
	// No path found, return as-is
	if suffix != "" && !strings.HasSuffix(uri, suffix) {
		return uri + suffix
	}
	return uri
}
