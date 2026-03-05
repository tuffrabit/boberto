// Package tools provides the tool system for agent operations.
package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tuffrabit/boberto/internal/config"
	"github.com/tuffrabit/boberto/internal/fs"
)

// GrepTool searches file contents with regex patterns.
type GrepTool struct {
	sandbox *fs.Sandbox
}

// NewGrepTool creates a new grep tool.
func NewGrepTool(sandbox *fs.Sandbox) *GrepTool {
	return &GrepTool{sandbox: sandbox}
}

// Name returns the tool name.
func (t *GrepTool) Name() string {
	return "grep"
}

// Description returns the tool description.
func (t *GrepTool) Description() string {
	return "Search file contents using regular expressions. Returns matching lines with file paths and line numbers."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *GrepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regular expression pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory or file to search in (default: project root)",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Optional glob pattern to filter files (e.g., '*.go')",
			},
			"output_mode": map[string]any{
				"type":        "string",
				"description": "Output format: 'content' (default), 'files_with_matches', or 'count_matches'",
				"enum":        []string{"content", "files_with_matches", "count_matches"},
			},
		},
		"required": []string{"pattern"},
	}
}

// Match represents a single grep match.
type Match struct {
	Path    string
	LineNum int
	Line    string
}

// Execute runs the grep tool.
func (t *GrepTool) Execute(ctx context.Context, args map[string]any, whitelist config.Whitelist) (Result, error) {
	pattern, err := ExtractString(args, "pattern")
	if err != nil {
		return Failure(err.Error()), err
	}

	searchPath := "."
	if pathVal, ok := OptionalString(args, "path"); ok {
		searchPath = pathVal
	}

	globPattern := ""
	if globVal, ok := OptionalString(args, "glob"); ok {
		globPattern = globVal
	}

	outputMode := "content"
	if modeVal, ok := OptionalString(args, "output_mode"); ok {
		switch modeVal {
		case "content", "files_with_matches", "count_matches":
			outputMode = modeVal
		default:
			return Failure(fmt.Sprintf("invalid output_mode: %s", modeVal)), fmt.Errorf("invalid output_mode")
		}
	}

	// Compile the regex
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Failure(fmt.Sprintf("invalid regex pattern: %v", err)), nil
	}

	// Validate the search path
	validPath, err := t.sandbox.Validate(searchPath)
	if err != nil {
		return Failure(fmt.Sprintf("invalid path: %v", err)), nil
	}

	// Determine if it's a file or directory
	info, err := os.Stat(validPath)
	if err != nil {
		return Failure(fmt.Sprintf("cannot access path: %v", err)), nil
	}

	var matches []Match

	if info.IsDir() {
		// Search directory
		matches, err = t.searchDirectory(ctx, validPath, re, globPattern)
	} else {
		// Search single file
		fileMatches, err := t.searchFile(validPath, re)
		if err != nil {
			return Failure(fmt.Sprintf("error searching file: %v", err)), nil
		}
		matches = fileMatches
	}

	if err != nil {
		return Failure(fmt.Sprintf("search failed: %v", err)), nil
	}

	// Format output based on mode
	return t.formatOutput(matches, outputMode, searchPath)
}

// searchDirectory searches all files in a directory.
func (t *GrepTool) searchDirectory(ctx context.Context, dir string, re *regexp.Regexp, globPattern string) ([]Match, error) {
	var matches []Match

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue walking
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path for ignore checking
		relPath, err := filepath.Rel(t.sandbox.Root, path)
		if err != nil {
			return nil
		}

		// Check ignore patterns
		if t.sandbox.Ignore != nil && t.sandbox.Ignore.Match(relPath) {
			return nil
		}

		// Check glob pattern if specified
		if globPattern != "" {
			matched, _ := filepath.Match(globPattern, filepath.Base(path))
			if !matched {
				// Try matching against relative path
				matched, _ = filepath.Match(globPattern, relPath)
			}
			if !matched {
				return nil
			}
		}

		// Search the file
		fileMatches, err := t.searchFile(path, re)
		if err != nil {
			return nil // Skip files that can't be read
		}

		matches = append(matches, fileMatches...)
		return nil
	})

	return matches, err
}

// searchFile searches a single file.
func (t *GrepTool) searchFile(path string, re *regexp.Regexp) ([]Match, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var matches []Match
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			// Get relative path
			relPath, _ := filepath.Rel(filepath.Dir(path), path)
			if relPath == "." {
				relPath = filepath.Base(path)
			}
			// Actually we want path relative to sandbox root
			relPath, _ = filepath.Rel(filepath.Dir(path[:len(path)-len(relPath)+1]), path)

			// Fix: get proper relative path
			// We need the path relative to sandbox root
			// Since we don't have direct access, let's work with what we have
			matches = append(matches, Match{
				Path:    path,
				LineNum: lineNum,
				Line:    line,
			})
		}
	}

	return matches, scanner.Err()
}

// formatOutput formats the matches based on output mode.
func (t *GrepTool) formatOutput(matches []Match, mode, searchPath string) (Result, error) {
	switch mode {
	case "files_with_matches":
		// Unique file paths only
		seen := make(map[string]bool)
		var files []string
		for _, m := range matches {
			relPath, _ := filepath.Rel(t.sandbox.Root, m.Path)
			if !seen[relPath] {
				seen[relPath] = true
				files = append(files, relPath)
			}
		}
		if len(files) == 0 {
			return Success("No files matched."), nil
		}
		return Success(strings.Join(files, "\n")), nil

	case "count_matches":
		return Success(fmt.Sprintf("%d", len(matches))), nil

	default: // content
		if len(matches) == 0 {
			return Success("No matches found."), nil
		}

		var lines []string
		currentFile := ""
		for _, m := range matches {
			relPath, _ := filepath.Rel(t.sandbox.Root, m.Path)
			if relPath != currentFile {
				currentFile = relPath
				lines = append(lines, fmt.Sprintf("\n%s:", relPath))
			}
			lines = append(lines, fmt.Sprintf("%d:%s", m.LineNum, m.Line))
		}
		return Success(strings.Join(lines, "\n")), nil
	}
}

// IsSensitive returns false - grep is not a sensitive tool.
func (t *GrepTool) IsSensitive() bool {
	return false
}
