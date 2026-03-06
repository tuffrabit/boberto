// Package tools provides the tool system for agent operations.
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/tuffrabit/boberto/internal/config"
	"github.com/tuffrabit/boberto/internal/fs"
)

// ReadFileTool reads file contents with optional line limits.
type ReadFileTool struct {
	sandbox *fs.Sandbox
}

// NewReadFileTool creates a new read_file tool.
func NewReadFileTool(sandbox *fs.Sandbox) *ReadFileTool {
	return &ReadFileTool{sandbox: sandbox}
}

// Name returns the tool name.
func (t *ReadFileTool) Name() string {
	return "read_file"
}

// Description returns the tool description.
func (t *ReadFileTool) Description() string {
	return "Read the contents of a file. Can optionally limit to specific line ranges."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *ReadFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read (relative to project root)",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Line number to start reading from (1-indexed, default: 1)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read (default: read entire file)",
			},
		},
		"required": []string{"path"},
	}
}

// Execute runs the read_file tool.
func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any, whitelist config.Whitelist) (Result, error) {
	path, err := ExtractString(args, "path")
	if err != nil {
		return Failure(err.Error()), err
	}

	offset := 1
	if offsetVal, ok := OptionalInt(args, "offset"); ok {
		offset = offsetVal
		if offset < 1 {
			return Failure("offset must be at least 1"), fmt.Errorf("offset must be at least 1")
		}
	}

	limit := 0
	if limitVal, ok := OptionalInt(args, "limit"); ok {
		limit = limitVal
		if limit < 1 {
			return Failure("limit must be at least 1"), fmt.Errorf("limit must be at least 1")
		}
	}

	// Read the file
	content, err := t.sandbox.ReadFile(path)
	if err != nil {
		return Failure(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	// Convert to lines for offset/limit handling
	lines := strings.Split(string(content), "\n")

	// Apply offset
	if offset > len(lines) {
		return Failure(fmt.Sprintf("offset %d exceeds file length (%d lines)", offset, len(lines))), nil
	}
	lines = lines[offset-1:]

	// Apply limit
	if limit > 0 && limit < len(lines) {
		lines = lines[:limit]
	}

	result := strings.Join(lines, "\n")
	return Success(result), nil
}

// IsSensitive returns false - read_file is not a sensitive tool.
func (t *ReadFileTool) IsSensitive() bool {
	return false
}
