// Package agent provides the worker and reviewer agent implementations.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tuffrabit/boberto/internal/config"
	"github.com/tuffrabit/boberto/internal/debug"
	"github.com/tuffrabit/boberto/internal/fs"
	"github.com/tuffrabit/boberto/internal/llm"
	"github.com/tuffrabit/boberto/internal/tools"
)

// Worker represents the worker agent that implements PRD requirements.
type Worker struct {
	provider     llm.Provider
	modelConfig  config.ModelConfig
	sandbox      *fs.Sandbox
	projectDir   string
	debug        *debug.Logger

	// Token tracking
	tokensUsed   int
	bailLimit    int

	// Iteration tracking
	iteration    int
	wroteFileThisIteration bool

	// History mode
	history      bool
}

// WorkerOptions contains configuration options for the Worker.
type WorkerOptions struct {
	Provider     llm.Provider
	ModelConfig  config.ModelConfig
	Sandbox      *fs.Sandbox
	ProjectDir   string
	Debug        *debug.Logger
	Iteration    int
	History      bool
}

// NewWorker creates a new Worker agent.
func NewWorker(opts WorkerOptions) *Worker {
	bailLimit := int(float64(opts.ModelConfig.ContextWindow) * opts.ModelConfig.BailThreshold)
	
	dbg := opts.Debug
	if dbg == nil {
		dbg = debug.NewLogger(false)
	}
	
	return &Worker{
		provider:    opts.Provider,
		modelConfig: opts.ModelConfig,
		sandbox:     opts.Sandbox,
		projectDir:  opts.ProjectDir,
		debug:       dbg,
		tokensUsed:  0,
		bailLimit:   bailLimit,
		iteration:   opts.Iteration,
		wroteFileThisIteration: false,
		history:     opts.History,
	}
}

// Run executes the worker agent for one iteration.
// It returns true if the worker completed its work (no new file writes),
// or false if more work is needed.
func (w *Worker) Run(ctx context.Context) (bool, error) {
	w.wroteFileThisIteration = false

	// Load context files
	prdContent, err := w.loadPRD()
	if err != nil {
		return false, fmt.Errorf("failed to load PRD: %w", err)
	}

	var summaryContent string
	if w.iteration > 1 {
		summaryContent, _ = w.loadSummary()
	}

	var feedbackContent string
	if w.iteration > 1 {
		feedbackContent, _ = w.loadFeedback()
	}

	// Load project config for whitelist
	projectCfg, err := config.LoadProject(w.projectDir)
	if err != nil {
		return false, fmt.Errorf("failed to load project config: %w", err)
	}

	// Build system prompt
	systemPrompt := w.buildSystemPrompt(prdContent, summaryContent, feedbackContent, projectCfg.Whitelist)

	// Count initial tokens
	systemTokens, _ := w.provider.CountTokens(systemPrompt)
	w.tokensUsed = systemTokens

	w.debug.Section("WORKER PHASE - Iteration %d", w.iteration)
	w.debug.Log("System prompt tokens: %d", systemTokens)
	w.debug.Log("Bail limit: %d tokens", w.bailLimit)

	// Initialize conversation with a user message to start the task
	// The model requires at least one user message to respond
	messages := []llm.Message{
		{
			Role:    "user",
			Content: "Please analyze the PRD and start implementing the requirements. Begin by exploring the codebase to understand the current state, then make necessary changes.",
		},
	}

	// Tool definitions
	toolDefs := w.buildToolDefinitions(projectCfg.Whitelist)

	// Track pending tool calls for OpenAI-style conversation
	pendingToolCalls := []llm.ToolCall{}

	for {
		// Check if we're approaching the bail threshold before making the request
		if w.tokensUsed >= w.bailLimit {
			w.debug.Log("Approaching bail limit (%d/%d tokens), wrapping up...", w.tokensUsed, w.bailLimit)
			
			// Send a wrap-up message
			wrapUpMsg := llm.Message{
				Role:    "user",
				Content: "You are approaching the token limit for this iteration. Please wrap up your current work and write a SUMMARY.md describing what you've accomplished so far.",
			}
			messages = append(messages, wrapUpMsg)
			
			// Make one final request to get the summary
			req := llm.Request{
				Model:     w.modelConfig.Name,
				System:    systemPrompt,
				Messages:  messages,
				Tools:     toolDefs,
				MaxTokens: 4096,
			}
			
			resp, err := w.provider.Complete(ctx, req)
			if err != nil {
				return false, fmt.Errorf("LLM completion failed during wrap-up: %w", err)
			}
			
			w.tokensUsed += resp.Usage.TotalTokens
			
			// Process any final tool calls (likely write_file for SUMMARY.md)
			for _, tc := range resp.ToolCalls {
				if tc.Name == "write_file" && w.isSummaryFile(tc.Arguments) {
					w.executeToolCall(ctx, tc, projectCfg.Whitelist)
				}
			}
			
			// Write summary if not already written
			if !w.wroteSummary(resp.Content) {
				w.writeSummary(ctx, resp.Content)
			}
			
			return false, nil // Need another iteration
		}

		// Make request to LLM
		req := llm.Request{
			Model:     w.modelConfig.Name,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     toolDefs,
			MaxTokens: 4096,
		}

		w.debug.Log("Sending request to LLM...")

		resp, err := w.provider.Complete(ctx, req)
		if err != nil {
			return false, fmt.Errorf("LLM completion failed: %w", err)
		}

		w.tokensUsed += resp.Usage.TotalTokens

		w.debug.Log("Response received. Tokens used this exchange: %d (total: %d/%d)", 
			resp.Usage.TotalTokens, w.tokensUsed, w.bailLimit)
		w.debug.TokenStatus(w.tokensUsed, w.bailLimit)

		// Add assistant message with content and tool calls
		assistantMsg := llm.Message{
			Role:    "assistant",
			Content: resp.Content,
		}
		messages = append(messages, assistantMsg)

		// Track pending tool calls for response pairing
		pendingToolCalls = resp.ToolCalls

		// If no tool calls, the worker is done for this iteration
		if len(resp.ToolCalls) == 0 {
			// Write summary with the content
			summary := resp.Content
			if summary == "" {
				summary = fmt.Sprintf("Iteration %d completed. No actions were taken.", w.iteration)
			}
			if err := w.writeSummary(ctx, summary); err != nil {
				return false, fmt.Errorf("failed to write summary: %w", err)
			}
			return !w.wroteFileThisIteration, nil
		}

		// Execute tool calls and build tool result messages
		toolResults := make([]llm.Message, 0, len(resp.ToolCalls))
		
		for _, tc := range pendingToolCalls {
			result := w.executeToolCall(ctx, tc, projectCfg.Whitelist)
			
			resultJSON, _ := json.Marshal(result)
			toolMsg := llm.Message{
				Role:       "tool",
				Content:    string(resultJSON),
				ToolCallID: tc.ID,
			}
			toolResults = append(toolResults, toolMsg)
			
			// Count tokens for tool result
			resultTokens, _ := w.provider.CountTokens(string(resultJSON))
			w.tokensUsed += resultTokens
		}

		// Add all tool results to messages
		messages = append(messages, toolResults...)
		
		// Clear pending tool calls
		pendingToolCalls = nil
	}
}

// executeToolCall executes a single tool call and returns the result.
func (w *Worker) executeToolCall(ctx context.Context, tc llm.ToolCall, whitelist config.Whitelist) tools.Result {
	result, err := tools.Execute(ctx, tc.Name, tc.Arguments, whitelist)
	if err != nil {
		result = tools.Failure(err.Error())
	}

	// Track if we wrote a file this iteration
	if tc.Name == "write_file" {
		w.wroteFileThisIteration = true
	}

	w.debug.ToolExecution(tc.Name, tc.Arguments, result.Content, result.IsError)

	return result
}

// buildSystemPrompt builds the system prompt for the worker.
func (w *Worker) buildSystemPrompt(prd, summary, feedback string, whitelist config.Whitelist) string {
	var prompt strings.Builder

	prompt.WriteString("You are Boberto, a coding agent implementing product requirements.\n\n")
	prompt.WriteString("Your task is to implement the requirements in the PRD by:\n")
	prompt.WriteString("1. Reading relevant files to understand the codebase\n")
	prompt.WriteString("2. Writing or modifying files to implement features\n")
	prompt.WriteString("3. When finished with this iteration, your work will be summarized in SUMMARY.md\n\n")

	// Add available tools
	prompt.WriteString("Available tools:\n")
	availableTools := tools.GetAvailable(whitelist)
	for _, tool := range availableTools {
		sensitive := ""
		if tool.IsSensitive() {
			sensitive = " [SENSITIVE - requires whitelist]"
		}
		prompt.WriteString(fmt.Sprintf("- %s: %s%s\n", tool.Name(), tool.Description(), sensitive))
	}
	prompt.WriteString("\n")

	// Add PRD content
	prompt.WriteString("=== PRD ===\n")
	prompt.WriteString(prd)
	prompt.WriteString("\n=== END PRD ===\n\n")

	// Add previous summary if available
	if summary != "" {
		prompt.WriteString("=== PREVIOUS SUMMARY ===\n")
		prompt.WriteString(summary)
		prompt.WriteString("\n=== END PREVIOUS SUMMARY ===\n\n")
	}

	// Add feedback if available
	if feedback != "" {
		prompt.WriteString("=== FEEDBACK FROM REVIEWER ===\n")
		prompt.WriteString(feedback)
		prompt.WriteString("\n=== END FEEDBACK ===\n\n")
	}

	prompt.WriteString(fmt.Sprintf("This is iteration %d. Implement the PRD requirements. ", w.iteration))
	prompt.WriteString("Use the available tools to read, write, and modify files. ")
	prompt.WriteString("When you have finished making progress on this iteration, stop calling tools. ")
	prompt.WriteString("Your final response will be saved as SUMMARY.md.\n")

	return prompt.String()
}

// buildToolDefinitions returns tool definitions for the LLM.
func (w *Worker) buildToolDefinitions(whitelist config.Whitelist) []llm.ToolDefinition {
	availableTools := tools.GetAvailable(whitelist)
	defs := make([]llm.ToolDefinition, len(availableTools))
	
	for i, tool := range availableTools {
		defs[i] = llm.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		}
	}
	
	return defs
}

// loadPRD loads the PRD.md file content.
func (w *Worker) loadPRD() (string, error) {
	data, err := w.sandbox.ReadFile("PRD.md")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// loadSummary loads the previous SUMMARY.md if it exists.
func (w *Worker) loadSummary() (string, error) {
	data, err := w.sandbox.ReadFile("SUMMARY.md")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// loadFeedback loads the FEEDBACK.md if it exists.
func (w *Worker) loadFeedback() (string, error) {
	data, err := w.sandbox.ReadFile("FEEDBACK.md")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// writeSummary writes the SUMMARY.md file.
func (w *Worker) writeSummary(ctx context.Context, content string) error {
	w.debug.Log("Writing SUMMARY.md (%d bytes)", len(content))

	// If history mode is enabled, rotate the existing SUMMARY.md file
	if w.history {
		if err := w.rotateSummary(); err != nil {
			w.debug.Log("Warning: failed to rotate SUMMARY.md: %v", err)
		}
	}

	data := []byte(content)
	return w.sandbox.WriteFile("SUMMARY.md", data, 0644)
}

// rotateSummary rotates the existing SUMMARY.md to SUMMARY_N.md.
func (w *Worker) rotateSummary() error {
	// Check if SUMMARY.md exists
	_, err := w.sandbox.Stat("SUMMARY.md")
	if err != nil {
		// File doesn't exist, nothing to rotate
		return nil
	}

	// Find the next available iteration number
	iteration := 1
	for {
		historyPath := fmt.Sprintf("SUMMARY_%d.md", iteration)
		_, err := w.sandbox.Stat(historyPath)
		if err != nil {
			// File doesn't exist, we can use this iteration number
			break
		}
		iteration++
	}

	// Read the existing SUMMARY.md
	data, err := w.sandbox.ReadFile("SUMMARY.md")
	if err != nil {
		return fmt.Errorf("failed to read SUMMARY.md: %w", err)
	}

	// Write to the history file
	historyPath := fmt.Sprintf("SUMMARY_%d.md", iteration)
	if err := w.sandbox.WriteFile(historyPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", historyPath, err)
	}

	w.debug.Log("Rotated SUMMARY.md to %s", historyPath)
	return nil
}

// wroteSummary checks if the content appears to be a summary.
func (w *Worker) wroteSummary(content string) bool {
	// Simple check - could be improved
	return len(content) > 0
}

// isSummaryFile checks if a write_file call is writing SUMMARY.md.
func (w *Worker) isSummaryFile(args map[string]any) bool {
	path, ok := args["path"].(string)
	if !ok {
		return false
	}
	return filepath.Base(path) == "SUMMARY.md"
}

// truncate truncates a string to the specified length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
