// Package config handles global and project configuration loading.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GlobalConfigPath returns the path to the global config file.
func GlobalConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".boberto", "config.json")
}

// ModelConfig represents a single model configuration.
type ModelConfig struct {
	APIType             string                 `json:"api_type"`
	APIKey              string                 `json:"api_key"`
	URI                 string                 `json:"uri"`
	Name                string                 `json:"name"`
	Local               bool                   `json:"local"`
	Provider            string                 `json:"provider,omitempty"`
	ContextWindow       int                    `json:"context_window"`
	MaxTokens           int                    `json:"max_tokens"`
	BailThreshold       float64                `json:"bail_threshold"`
	SupportsToolCalling bool                   `json:"supports_tool_calling"`
	ExtraBody           map[string]interface{} `json:"extra_body,omitempty"`
}

// DefaultMaxTokens is the default maximum tokens for model responses.
const DefaultMaxTokens = 4096

// GetMaxTokens returns the max_tokens value for this model config.
// If the config value is 0 or negative, it returns the default (4096).
func (m ModelConfig) GetMaxTokens() int {
	if m.MaxTokens <= 0 {
		return DefaultMaxTokens
	}
	return m.MaxTokens
}

// WorkerConfig represents worker agent configuration.
type WorkerConfig struct {
	DefaultModel string `json:"default_model"`
}

// ReviewerConfig represents reviewer agent configuration.
type ReviewerConfig struct {
	DefaultModel string `json:"default_model"`
}

// Global represents the global configuration stored in ~/.boberto/config.json.
type Global struct {
	Models   map[string]ModelConfig `json:"models"`
	Worker   WorkerConfig           `json:"worker"`
	Reviewer ReviewerConfig         `json:"reviewer"`
}

// DefaultGlobal returns a default global configuration.
func DefaultGlobal() Global {
	return Global{
		Models: map[string]ModelConfig{
			"qwen2.5-coder": {
				APIType:             "openai",
				APIKey:              "not-needed",
				URI:                 "http://localhost:1234/v1/chat/completions",
				Name:                "qwen2.5-coder-14b",
				Local:               true,
				Provider:            "lmstudio",
				ContextWindow:       32768,
				BailThreshold:       0.75,
				SupportsToolCalling: true,
			},
			"llama3.3-reviewer": {
				APIType:             "openai",
				APIKey:              "not-needed",
				URI:                 "http://localhost:11434/v1/chat/completions",
				Name:                "llama3.3",
				Local:               true,
				Provider:            "ollama",
				ContextWindow:       128000,
				BailThreshold:       0.85,
				SupportsToolCalling: false,
			},
			"gpt-4o": {
				APIType:             "openai",
				APIKey:              "",
				URI:                 "https://api.openai.com/v1/chat/completions",
				Name:                "gpt-4o",
				Local:               false,
				ContextWindow:       128000,
				BailThreshold:       0.80,
				SupportsToolCalling: true,
			},
			"claude-sonnet": {
				APIType:             "anthropic",
				APIKey:              "",
				URI:                 "https://api.anthropic.com/v1/messages",
				Name:                "claude-3-5-sonnet-20241022",
				Local:               false,
				ContextWindow:       200000,
				BailThreshold:       0.80,
				SupportsToolCalling: true,
			},
		},
		Worker: WorkerConfig{
			DefaultModel: "qwen2.5-coder",
		},
		Reviewer: ReviewerConfig{
			DefaultModel: "llama3.3-reviewer",
		},
	}
}

// LoadGlobal loads the global configuration from ~/.boberto/config.json.
// If the file does not exist, it creates a default one.
func LoadGlobal() (Global, error) {
	path := GlobalConfigPath()
	if path == "" {
		return Global{}, fmt.Errorf("could not determine home directory")
	}

	// Check if config exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create default config
		if err := createDefaultGlobalConfig(path); err != nil {
			return Global{}, fmt.Errorf("failed to create default config: %w", err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Global{}, fmt.Errorf("failed to read global config: %w", err)
	}

	var cfg Global
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Global{}, fmt.Errorf("failed to parse global config: %w", err)
	}

	return cfg, nil
}

// createDefaultGlobalConfig creates the default global config file.
func createDefaultGlobalConfig(path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	cfg := DefaultGlobal()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal default config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write default config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Created default config at %s\n", path)
	return nil
}

// GetModel returns a model config by name, or an error if not found.
func (g Global) GetModel(name string) (ModelConfig, error) {
	model, ok := g.Models[name]
	if !ok {
		return ModelConfig{}, fmt.Errorf("model not found: %s", name)
	}
	return model, nil
}
