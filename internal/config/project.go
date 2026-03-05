// Package config handles global and project configuration loading.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Whitelist represents the whitelist configuration for sensitive tools.
type Whitelist struct {
	Bash      []string `json:"bash"`
	WebSearch bool     `json:"web_search"`
	WebFetch  []string `json:"web_fetch"`
}

// AllowsBash checks if a command is allowed by the whitelist.
// For now, does exact matching. Could be extended to pattern matching.
func (w Whitelist) AllowsBash(command string) bool {
	for _, allowed := range w.Bash {
		if allowed == command {
			return true
		}
	}
	return false
}

// AllowsWebFetch checks if a URL is allowed by the whitelist.
// For now, does exact matching. Could be extended to pattern matching.
func (w Whitelist) AllowsWebFetch(url string) bool {
	for _, allowed := range w.WebFetch {
		if allowed == url {
			return true
		}
	}
	return false
}

// Project represents the project configuration stored in .boberto/config.json.
// This is loaded at the start of each iteration (hot-reloadable).
type Project struct {
	Ignore    []string  `json:"ignore"`
	Whitelist Whitelist `json:"whitelist"`
}

// DefaultProject returns a default project configuration.
func DefaultProject() Project {
	return Project{
		Ignore: []string{
			"node_modules/**",
			"*.log",
			".git/**",
			"dist/**",
			"*.tmp",
		},
		Whitelist: Whitelist{
			Bash:      []string{},
			WebSearch: false,
			WebFetch:  []string{},
		},
	}
}

// LoadProject loads the project configuration from <projectRoot>/.boberto/config.json.
// If the file does not exist, returns the default project config.
func LoadProject(projectRoot string) (Project, error) {
	path := filepath.Join(projectRoot, ".boberto", "config.json")

	// Check if config exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return DefaultProject(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Project{}, fmt.Errorf("failed to read project config: %w", err)
	}

	var cfg Project
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Log error but return defaults
		fmt.Fprintf(os.Stderr, "Warning: failed to parse project config: %v\n", err)
		return DefaultProject(), nil
	}

	return cfg, nil
}

// ProjectConfigPath returns the path to the project config file.
func ProjectConfigPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".boberto", "config.json")
}
