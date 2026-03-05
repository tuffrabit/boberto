// Package debug provides comprehensive debug logging for the Ralph Loop.
package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Logger provides comprehensive debug output for the agent.
type Logger struct {
	enabled bool
	mu      sync.Mutex
	indent  string
}

// NewLogger creates a new debug logger.
func NewLogger(enabled bool) *Logger {
	return &Logger{
		enabled: enabled,
		indent:  "",
	}
}

// IsEnabled returns whether debug logging is enabled.
func (l *Logger) IsEnabled() bool {
	return l.enabled
}

// Section prints a section header.
func (l *Logger) Section(format string, args ...interface{}) {
	if !l.enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stdout, "\n[%s] ═══════════════════════════════════════════════════════════════\n", timestamp)
	fmt.Fprintf(os.Stdout, "[%s] %s\n", timestamp, fmt.Sprintf(format, args...))
	fmt.Fprintf(os.Stdout, "[%s] ═══════════════════════════════════════════════════════════════\n", timestamp)
}

// Log prints a debug message.
func (l *Logger) Log(format string, args ...interface{}) {
	if !l.enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stdout, "[%s] %s%s\n", timestamp, l.indent, fmt.Sprintf(format, args...))
}

// LogMultiLine prints a multi-line debug message with proper indentation.
func (l *Logger) LogMultiLine(prefix, content string) {
	if !l.enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	
	timestamp := time.Now().Format("15:04:05.000")
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fmt.Fprintf(os.Stdout, "[%s] %s%s%s\n", timestamp, l.indent, prefix, line)
	}
}

// JSON logs a JSON object with pretty printing.
func (l *Logger) JSON(label string, data interface{}) {
	if !l.enabled {
		return
	}
	
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		l.Log("%s: [error encoding JSON: %v]", label, err)
		return
	}
	
	l.Section("%s", label)
	l.LogMultiLine("", strings.TrimRight(buf.String(), "\n"))
}

// Request logs an LLM request with full details.
func (l *Logger) Request(model, system string, messages []map[string]interface{}, tools []map[string]interface{}) {
	if !l.enabled {
		return
	}
	
	l.Section("LLM REQUEST → %s", model)
	
	if system != "" {
		l.Log("SYSTEM PROMPT:")
		l.LogMultiLine("  ", truncate(system, 2000))
		l.Log("")
	}
	
	if len(messages) > 0 {
		l.Log("CONVERSATION HISTORY (%d messages):", len(messages))
		for i, msg := range messages {
			role, _ := msg["role"].(string)
			content, _ := msg["content"].(string)
			toolCallID, _ := msg["tool_call_id"].(string)
			
			l.Log("  [%d] Role: %s", i, role)
			if toolCallID != "" {
				l.Log("      ToolCallID: %s", toolCallID)
			}
			if content != "" {
				l.LogMultiLine("      ", truncate(content, 500))
			}
		}
		l.Log("")
	}
	
	if len(tools) > 0 {
		l.Log("AVAILABLE TOOLS (%d):", len(tools))
		for i, tool := range tools {
			name, _ := tool["name"].(string)
			desc, _ := tool["description"].(string)
			l.Log("  [%d] %s: %s", i, name, truncate(desc, 100))
		}
		l.Log("")
	}
}

// Response logs an LLM response with full details.
func (l *Logger) Response(content string, toolCalls []map[string]interface{}, usage map[string]int) {
	if !l.enabled {
		return
	}
	
	l.Section("LLM RESPONSE")
	
	if content != "" {
		l.Log("CONTENT:")
		l.LogMultiLine("  ", truncate(content, 2000))
		l.Log("")
	}
	
	if len(toolCalls) > 0 {
		l.Log("TOOL CALLS (%d):", len(toolCalls))
		for i, tc := range toolCalls {
			id, _ := tc["id"].(string)
			name, _ := tc["name"].(string)
			args, _ := tc["arguments"].(map[string]interface{})
			
			l.Log("  [%d] ID: %s", i, id)
			l.Log("      Name: %s", name)
			if len(args) > 0 {
				l.Log("      Arguments:")
				argsJSON, _ := json.MarshalIndent(args, "        ", "  ")
				l.LogMultiLine("        ", string(argsJSON))
			}
		}
		l.Log("")
	}
	
	if usage != nil {
		l.Log("TOKEN USAGE:")
		if input, ok := usage["input"]; ok {
			l.Log("  Input tokens:  %d", input)
		}
		if output, ok := usage["output"]; ok {
			l.Log("  Output tokens: %d", output)
		}
		if total, ok := usage["total"]; ok {
			l.Log("  Total tokens:  %d", total)
		}
		l.Log("")
	}
}

// ToolExecution logs a tool execution.
func (l *Logger) ToolExecution(name string, args map[string]interface{}, result string, isError bool) {
	if !l.enabled {
		return
	}
	
	l.Section("TOOL EXECUTION: %s", name)
	
	if len(args) > 0 {
		l.Log("ARGUMENTS:")
		argsJSON, _ := json.MarshalIndent(args, "  ", "  ")
		l.LogMultiLine("  ", string(argsJSON))
		l.Log("")
	}
	
	if isError {
		l.Log("RESULT (ERROR):")
	} else {
		l.Log("RESULT:")
	}
	l.LogMultiLine("  ", truncate(result, 2000))
	l.Log("")
}

// TokenStatus logs current token usage status.
func (l *Logger) TokenStatus(used, limit int) {
	if !l.enabled {
		return
	}
	
	percentage := float64(used) / float64(limit) * 100
	l.Log("Token usage: %d/%d (%.1f%%)", used, limit, percentage)
}

// PushIndent increases the indentation level.
func (l *Logger) PushIndent() {
	if !l.enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.indent += "  "
}

// PopIndent decreases the indentation level.
func (l *Logger) PopIndent() {
	if !l.enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.indent) >= 2 {
		l.indent = l.indent[:len(l.indent)-2]
	}
}

// truncate truncates a string to the specified length, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated]"
}
