// Package tools provides the tool system for agent operations.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuffrabit/boberto/internal/config"
	"github.com/tuffrabit/boberto/internal/fs"
)

// GlobTool finds files matching a glob pattern.
type GlobTool struct {
	sandbox *fs.Sandbox
}

// NewGlobTool creates a new glob tool.
func NewGlobTool(sandbox *fs.Sandbox) *GlobTool {
	return &GlobTool{sandbox: sandbox}
}

// Name returns the tool name.
func (t *GlobTool) Name() string {
	return "glob"
}

// Description returns the tool description.
func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern. Supports * (any characters) and ** (recursive). Returns matching file paths."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *GlobTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern to match (e.g., '*.go', '**/*.md', 'src/*')",
			},
		},
		"required": []string{"pattern"},
	}
}

// Execute runs the glob tool.
func (t *GlobTool) Execute(ctx context.Context, args map[string]any, whitelist config.Whitelist) (Result, error) {
	pattern, err := ExtractString(args, "pattern")
	if err != nil {
		return Failure(err.Error()), err
	}

	// Clean the pattern
	pattern = filepath.Clean(pattern)

	// Reject patterns with path traversal
	if strings.Contains(pattern, "..") {
		return Failure("pattern cannot contain path traversal (..)"), fmt.Errorf("pattern cannot contain path traversal")
	}

	// Check if pattern is absolute and outside sandbox
	if filepath.IsAbs(pattern) {
		return Failure("pattern must be relative to project root"), fmt.Errorf("pattern must be relative to project root")
	}

	// Convert ** to a format filepath.Glob can handle
	// filepath.Glob doesn't support **, so we need to handle it manually
	var matches []string

	if strings.Contains(pattern, "**") {
		matches, err = t.globRecursive(pattern)
	} else {
		// Simple glob - use filepath.Glob
		fullPattern := filepath.Join(t.sandbox.Root, pattern)
		fileMatches, err := filepath.Glob(fullPattern)
		if err != nil {
			return Failure(fmt.Sprintf("invalid pattern: %v", err)), nil
		}

		// Convert to relative paths and filter by ignore patterns
		for _, match := range fileMatches {
			relPath, err := filepath.Rel(t.sandbox.Root, match)
			if err != nil {
				continue
			}
			if t.sandbox.Ignore != nil && t.sandbox.Ignore.Match(relPath) {
				continue
			}
			matches = append(matches, relPath)
		}
	}

	if err != nil {
		return Failure(fmt.Sprintf("glob failed: %v", err)), nil
	}

	// Format result
	if len(matches) == 0 {
		return Success("No files matched the pattern."), nil
	}

	result := strings.Join(matches, "\n")
	return Success(fmt.Sprintf("Found %d file(s):\n%s", len(matches), result)), nil
}

// globRecursive handles ** patterns by walking the directory tree.
func (t *GlobTool) globRecursive(pattern string) ([]string, error) {
	// Split pattern by **
	parts := strings.Split(pattern, "**")

	if len(parts) == 1 {
		// No **, use regular glob
		fullPattern := filepath.Join(t.sandbox.Root, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return nil, err
		}
		var result []string
		for _, match := range matches {
			relPath, _ := filepath.Rel(t.sandbox.Root, match)
			if t.sandbox.Ignore != nil && t.sandbox.Ignore.Match(relPath) {
				continue
			}
			result = append(result, relPath)
		}
		return result, nil
	}

	// Handle **/suffix pattern (most common case)
	if strings.HasPrefix(pattern, "**") {
		suffix := parts[1]
		if strings.HasPrefix(suffix, "/") {
			suffix = suffix[1:]
		}
		return t.findWithSuffix(suffix)
	}

	// Handle prefix/**/suffix pattern
	if len(parts) == 2 {
		prefix := parts[0]
		suffix := parts[1]
		if strings.HasSuffix(prefix, "/") {
			prefix = prefix[:len(prefix)-1]
		}
		if strings.HasPrefix(suffix, "/") {
			suffix = suffix[1:]
		}
		return t.findWithPrefixAndSuffix(prefix, suffix)
	}

	// Multiple ** not supported
	return nil, fmt.Errorf("patterns with multiple ** are not fully supported")
}

// findWithSuffix finds files matching **/suffix pattern.
func (t *GlobTool) findWithSuffix(suffix string) ([]string, error) {
	var matches []string

	err := filepath.Walk(t.sandbox.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue walking
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(t.sandbox.Root, path)
		if err != nil {
			return nil
		}

		// Check ignore patterns
		if t.sandbox.Ignore != nil && t.sandbox.Ignore.Match(relPath) {
			return nil
		}

		// Check if file matches suffix pattern
		matched, err := filepath.Match(suffix, relPath)
		if err != nil {
			return nil
		}
		if !matched {
			// Also try matching just the filename
			matched, _ = filepath.Match(suffix, filepath.Base(relPath))
		}

		if matched {
			matches = append(matches, relPath)
		}

		return nil
	})

	return matches, err
}

// findWithPrefixAndSuffix finds files matching prefix/**/suffix pattern.
func (t *GlobTool) findWithPrefixAndSuffix(prefix, suffix string) ([]string, error) {
	startDir := filepath.Join(t.sandbox.Root, prefix)

	// Check if start directory exists
	if _, err := os.Stat(startDir); os.IsNotExist(err) {
		return []string{}, nil
	}

	var matches []string

	err := filepath.Walk(startDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue walking
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path from sandbox root
		relPath, err := filepath.Rel(t.sandbox.Root, path)
		if err != nil {
			return nil
		}

		// Check ignore patterns
		if t.sandbox.Ignore != nil && t.sandbox.Ignore.Match(relPath) {
			return nil
		}

		// Get relative path from the prefix directory
		relFromPrefix, err := filepath.Rel(startDir, path)
		if err != nil {
			return nil
		}

		// Check if file matches suffix pattern
		matched, err := filepath.Match(suffix, relFromPrefix)
		if err != nil {
			return nil
		}
		if !matched {
			// Also try matching just the filename
			matched, _ = filepath.Match(suffix, filepath.Base(relFromPrefix))
		}

		if matched {
			matches = append(matches, relPath)
		}

		return nil
	})

	return matches, err
}

// IsSensitive returns false - glob is not a sensitive tool.
func (t *GlobTool) IsSensitive() bool {
	return false
}
