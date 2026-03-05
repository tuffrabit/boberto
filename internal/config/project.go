// Package config handles global and project configuration loading.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Whitelist represents the whitelist configuration for sensitive tools.
type Whitelist struct {
	Bash      []string `json:"bash"`
	WebSearch bool     `json:"web_search"`
	WebFetch  []string `json:"web_fetch"`
}

// AllowsBash checks if a command is allowed by the whitelist.
// Supports glob patterns: * matches any sequence, ? matches single char.
// The command must match the start of an allowed pattern (prefix match).
// Examples:
//   - "go test" allows "go test", "go test ./..."
//   - "go build*" allows "go build", "go build ./cmd/app"
//   - "make*" allows "make", "make build", "make test"
func (w Whitelist) AllowsBash(command string) bool {
	for _, pattern := range w.Bash {
		if matchBashPattern(pattern, command) {
			return true
		}
	}
	return false
}

// matchBashPattern matches a command against an allowed pattern.
// Patterns support glob-style wildcards.
func matchBashPattern(pattern, command string) bool {
	pattern = strings.TrimSpace(pattern)
	command = strings.TrimSpace(command)

	// Exact match
	if pattern == command {
		return true
	}

	// For bash patterns, we want prefix matching behavior:
	// Pattern "go test" should match "go test ./..."
	// So we check if the command starts with the non-wildcard part of the pattern
	
	// Extract the non-wildcard prefix from the pattern
	prefix := pattern
	if idx := strings.IndexAny(pattern, "*?["); idx != -1 {
		prefix = strings.TrimSpace(pattern[:idx])
	}

	// If command starts with the prefix, check glob match
	if strings.HasPrefix(command, prefix) {
		// Create a matcher for the pattern against the command
		// We need to match the pattern as a glob against the command
		return matchGlobPattern(pattern, command)
	}

	return false
}

// matchGlobPattern matches a glob pattern against a string.
func matchGlobPattern(pattern, str string) bool {
	// Simple cases
	if pattern == "*" {
		return true
	}
	if pattern == str {
		return true
	}

	// Use dynamic programming for glob matching
	pLen := len(pattern)
	sLen := len(str)

	dp := make([]bool, sLen+1)
	dp[0] = true

	// Handle pattern starting with *
	for i := 0; i < pLen && pattern[i] == '*'; i++ {
		for j := 0; j <= sLen; j++ {
			dp[j] = true
		}
	}

	for i := 0; i < pLen; i++ {
		newDp := make([]bool, sLen+1)
		p := pattern[i]

		for j := 0; j < sLen; j++ {
			if !dp[j] {
				continue
			}

			switch p {
			case '*':
				for k := j; k <= sLen; k++ {
					newDp[k] = true
				}
			case '?':
				newDp[j+1] = true
			default:
				if p == str[j] {
					newDp[j+1] = true
				}
			}
		}

		if p == '*' && dp[sLen] {
			newDp[sLen] = true
		}

		dp = newDp
	}

	return dp[sLen]
}

// AllowsWebFetch checks if a URL is allowed by the whitelist.
// Supports glob patterns for matching URL patterns.
// Examples:
//   - "https://api.github.com/repos/*" matches any GitHub API repo URL
//   - "https://docs.rs/**" matches any docs.rs URL and subpaths
func (w Whitelist) AllowsWebFetch(url string) bool {
	for _, pattern := range w.WebFetch {
		if matchGlobPattern(pattern, url) {
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
