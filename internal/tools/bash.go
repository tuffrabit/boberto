// Package tools provides the tool system for agent operations.
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/tuffrabit/boberto/internal/config"
)

// BashTool executes shell commands (sensitive tool).
type BashTool struct {
	timeout time.Duration
}

// NewBashTool creates a new bash tool with the given timeout.
func NewBashTool(timeout time.Duration) *BashTool {
	if timeout <= 0 {
		timeout = 60 * time.Second // Default 60 second timeout
	}
	return &BashTool{timeout: timeout}
}

// Name returns the tool name.
func (t *BashTool) Name() string {
	return "bash"
}

// Description returns the tool description.
func (t *BashTool) Description() string {
	return "Execute a shell command. Commands must be whitelisted in project config. Use with caution."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *BashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default: 60, max: 300)",
			},
		},
		"required": []string{"command"},
	}
}

// Execute runs the bash tool.
// CRITICAL: This tool validates the command against the whitelist before executing.
func (t *BashTool) Execute(ctx context.Context, args map[string]any, whitelist config.Whitelist) (Result, error) {
	command, err := ExtractString(args, "command")
	if err != nil {
		return Failure(err.Error()), err
	}

	// Validate command against whitelist
	if !whitelist.AllowsBash(command) {
		return Failure(fmt.Sprintf("command not in whitelist: %s", command)), fmt.Errorf("command not in whitelist")
	}

	// Get timeout
	timeout := t.timeout
	if timeoutVal, ok := OptionalInt(args, "timeout"); ok {
		if timeoutVal > 0 && timeoutVal <= 300 {
			timeout = time.Duration(timeoutVal) * time.Second
		}
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute the command
	cmd := exec.CommandContext(execCtx, "sh", "-c", command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Build result
	var output strings.Builder

	if stdout.Len() > 0 {
		output.WriteString(stdout.String())
	}

	if stderr.Len() > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString("STDERR:\n")
		output.WriteString(stderr.String())
	}

	// Handle different exit scenarios
	if runErr != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return Failure(fmt.Sprintf("command timed out after %v", timeout)), nil
		}

		// Command failed but we still return output
		result := output.String()
		if result == "" {
			result = fmt.Sprintf("Command failed: %v", runErr)
		} else {
			result = fmt.Sprintf("Exit error: %v\n\n%s", runErr, result)
		}
		return Failure(result), nil
	}

	result := output.String()
	if result == "" {
		result = "Command executed successfully (no output)."
	}

	return Success(result), nil
}

// IsSensitive returns true - bash is a sensitive tool requiring whitelist.
func (t *BashTool) IsSensitive() bool {
	return true
}
