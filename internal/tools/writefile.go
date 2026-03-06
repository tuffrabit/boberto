// Package tools provides the tool system for agent operations.
package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tuffrabit/boberto/internal/config"
	"github.com/tuffrabit/boberto/internal/fs"
)

// WriteFileTool writes or appends to files within the sandbox.
type WriteFileTool struct {
	sandbox *fs.Sandbox
}

// NewWriteFileTool creates a new write_file tool.
func NewWriteFileTool(sandbox *fs.Sandbox) *WriteFileTool {
	return &WriteFileTool{sandbox: sandbox}
}

// Name returns the tool name.
func (t *WriteFileTool) Name() string {
	return "write_file"
}

// Description returns the tool description.
func (t *WriteFileTool) Description() string {
	return "Write content to a file. Can optionally append instead of overwrite. Creates parent directories as needed."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *WriteFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write (relative to project root)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file. IMPORTANT: For files larger than ~8000 characters, use append=true and write in multiple smaller chunks.",
			},
			"append": map[string]any{
				"type":        "boolean",
				"description": "If true, append to the file instead of overwriting (default: false)",
			},
		},
		"required": []string{"path", "content"},
	}
}

// Execute runs the write_file tool.
func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any, whitelist config.Whitelist) (Result, error) {
	path, err := ExtractString(args, "path")
	if err != nil {
		return Failure(err.Error()), err
	}

	content, err := ExtractString(args, "content")
	if err != nil {
		return Failure(err.Error()), err
	}

	// Validate content doesn't appear to be truncated/corrupted
	// Check for common signs of mid-generation truncation
	if len(content) > 100 {
		// Check for unclosed strings, incomplete CSS properties, etc.
		unclosedQuotes := strings.Count(content, "\"")%2 != 0
		unclosedSingleQuotes := strings.Count(content, "'")%2 != 0
		unclosedBackticks := strings.Count(content, "`")%2 != 0
		
		// Check for incomplete CSS/JS patterns at the end
		trimmed := strings.TrimSpace(content)
		if len(trimmed) > 0 {
			lastChar := trimmed[len(trimmed)-1]
			// Check if content ends mid-statement (with an operator or opening brace)
			endsMidStatement := lastChar == ':' || lastChar == ',' || lastChar == '+' || 
				lastChar == '-' || lastChar == '*' || lastChar == '/' || lastChar == '=' ||
				lastChar == '(' || lastChar == '{' || lastChar == '['
			
			if unclosedQuotes || unclosedSingleQuotes || unclosedBackticks || endsMidStatement {
				return Failure("Content appears to be truncated or incomplete (unclosed quotes or incomplete statement). " +
					"Please use smaller chunks with append=true when writing large files."), nil
			}
		}
	}

	appendMode := false
	if appendVal, ok := ExtractBool(args, "append"); ok {
		appendMode = appendVal
	}

	// Validate the path first
	validPath, err := t.sandbox.Validate(path)
	if err != nil {
		return Failure(fmt.Sprintf("invalid path: %v", err)), nil
	}

	var writeErr error
	if appendMode {
		// Append mode
		file, err := os.OpenFile(validPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return Failure(fmt.Sprintf("failed to open file: %v", err)), nil
		}
		defer file.Close()

		_, writeErr = file.WriteString(content)
	} else {
		// Overwrite mode - use sandbox's WriteFile
		writeErr = t.sandbox.WriteFile(path, []byte(content), 0644)
	}

	if writeErr != nil {
		return Failure(fmt.Sprintf("failed to write file: %v", writeErr)), nil
	}

	mode := "wrote"
	if appendMode {
		mode = "appended to"
	}

	return Success(fmt.Sprintf("Successfully %s %s (%d bytes)", mode, path, len(content))), nil
}

// IsSensitive returns false - write_file is not a sensitive tool.
func (t *WriteFileTool) IsSensitive() bool {
	return false
}
