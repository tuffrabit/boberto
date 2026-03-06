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

// Reviewer represents the reviewer agent that reviews worker output.
type Reviewer struct {
	provider    llm.Provider
	modelConfig config.ModelConfig
	sandbox     *fs.Sandbox
	projectDir  string
	debug       *debug.Logger

	// Token tracking
	tokensUsed int
	bailLimit  int

	// Iteration tracking
	iteration int

	// History mode
	history   bool
}

// ReviewerOptions contains configuration options for the Reviewer.
type ReviewerOptions struct {
	Provider    llm.Provider
	ModelConfig config.ModelConfig
	Sandbox     *fs.Sandbox
	ProjectDir  string
	Debug       *debug.Logger
	Iteration   int
	History     bool
}

// NewReviewer creates a new Reviewer agent.
func NewReviewer(opts ReviewerOptions) *Reviewer {
	bailLimit := int(float64(opts.ModelConfig.ContextWindow) * opts.ModelConfig.BailThreshold)

	dbg := opts.Debug
	if dbg == nil {
		dbg = debug.NewLogger(false)
	}

	return &Reviewer{
		provider:    opts.Provider,
		modelConfig: opts.ModelConfig,
		sandbox:     opts.Sandbox,
		projectDir:  opts.ProjectDir,
		debug:       dbg,
		tokensUsed:  0,
		bailLimit:   bailLimit,
		iteration:   opts.Iteration,
		history:     opts.History,
	}
}

// ReviewResult contains the result of a review.
type ReviewResult struct {
	// LGTM is true if the reviewer approves (no feedback needed)
	LGTM bool
	// Feedback contains the review feedback (empty if LGTM)
	Feedback string
}

// Run executes the reviewer agent.
// It reviews the worker's output and generates FEEDBACK.md.
// Returns LGTM=true if the review passes (empty feedback file).
func (r *Reviewer) Run(ctx context.Context) (*ReviewResult, error) {
	// Load project config for whitelist
	projectCfg, err := config.LoadProject(r.projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load project config: %w", err)
	}

	// Check if model supports tool calling
	if r.modelConfig.SupportsToolCalling {
		return r.runToolMode(ctx, projectCfg.Whitelist)
	}
	return r.runMediatedMode(ctx, projectCfg.Whitelist)
}

// runToolMode runs the reviewer with tool calling support.
func (r *Reviewer) runToolMode(ctx context.Context, whitelist config.Whitelist) (*ReviewResult, error) {
	// Load context files
	prdContent, err := r.loadPRD()
	if err != nil {
		return nil, fmt.Errorf("failed to load PRD: %w", err)
	}

	summaryContent, err := r.loadSummary()
	if err != nil {
		return nil, fmt.Errorf("failed to load SUMMARY.md: %w", err)
	}

	// Build system prompt
	systemPrompt := r.buildSystemPromptToolMode(prdContent, summaryContent, whitelist)

	// Count initial tokens
	systemTokens, _ := r.provider.CountTokens(systemPrompt)
	r.tokensUsed = systemTokens

	r.debug.Section("REVIEWER PHASE - Iteration %d", r.iteration)
	r.debug.Log("System prompt tokens: %d", systemTokens)
	r.debug.Log("Bail limit: %d tokens", r.bailLimit)

	// Initialize conversation with a user message to start the review
	// The model requires at least one user message to respond
	messages := []llm.Message{
		{
			Role:    "user",
			Content: "Please review the worker's implementation against the PRD requirements. Analyze the changes made and provide constructive feedback.",
		},
	}

	// Tool definitions
	toolDefs := r.buildToolDefinitions(whitelist)

	var feedback strings.Builder

	for {
		// Check if we're approaching the bail threshold
		if r.tokensUsed >= r.bailLimit {
			r.debug.Log("Approaching bail limit (%d/%d tokens), wrapping up...", r.tokensUsed, r.bailLimit)

			// Send a wrap-up message
			wrapUpMsg := llm.Message{
				Role:    "user",
				Content: "You are approaching the token limit for this review. Please wrap up your review and provide your final feedback now.",
			}
			messages = append(messages, wrapUpMsg)

			// Make one final request to get the feedback
			req := llm.Request{
				Model:     r.modelConfig.Name,
				System:    systemPrompt,
				Messages:  messages,
				Tools:     toolDefs,
				MaxTokens: 4096,
			}

			resp, err := r.provider.Complete(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("LLM completion failed during wrap-up: %w", err)
			}

			r.tokensUsed += resp.Usage.TotalTokens
			feedback.WriteString(resp.Content)

			// Write feedback and return
			if err := r.writeFeedback(ctx, feedback.String()); err != nil {
				return nil, fmt.Errorf("failed to write feedback: %w", err)
			}

			return r.createResult(feedback.String()), nil
		}

		// Make request to LLM
		req := llm.Request{
			Model:     r.modelConfig.Name,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     toolDefs,
			MaxTokens: 4096,
		}

		r.debug.Log("Sending request to LLM...")

		resp, err := r.provider.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("LLM completion failed: %w", err)
		}

		r.tokensUsed += resp.Usage.TotalTokens

		r.debug.Log("Response received. Tokens used this exchange: %d (total: %d/%d)",
			resp.Usage.TotalTokens, r.tokensUsed, r.bailLimit)
		r.debug.TokenStatus(r.tokensUsed, r.bailLimit)

		// Add assistant message
		assistantMsg := llm.Message{
			Role:    "assistant",
			Content: resp.Content,
		}
		messages = append(messages, assistantMsg)

		// Accumulate any content as feedback
		feedback.WriteString(resp.Content)

		// If no tool calls, the reviewer is done
		if len(resp.ToolCalls) == 0 {
			// Write feedback file
			if err := r.writeFeedback(ctx, feedback.String()); err != nil {
				return nil, fmt.Errorf("failed to write feedback: %w", err)
			}
			return r.createResult(feedback.String()), nil
		}

		// Execute tool calls and build tool result messages
		toolResults := make([]llm.Message, 0, len(resp.ToolCalls))

		for _, tc := range resp.ToolCalls {
			result := r.executeToolCall(ctx, tc, whitelist)

			resultJSON, _ := json.Marshal(result)
			toolMsg := llm.Message{
				Role:       "tool",
				Content:    string(resultJSON),
				ToolCallID: tc.ID,
			}
			toolResults = append(toolResults, toolMsg)

			// Count tokens for tool result
			resultTokens, _ := r.provider.CountTokens(string(resultJSON))
			r.tokensUsed += resultTokens
		}

		// Add all tool results to messages
		messages = append(messages, toolResults...)
	}
}

// runMediatedMode runs the reviewer without tool calling (Boberto-mediated).
func (r *Reviewer) runMediatedMode(ctx context.Context, whitelist config.Whitelist) (*ReviewResult, error) {
	// Load context files
	prdContent, err := r.loadPRD()
	if err != nil {
		return nil, fmt.Errorf("failed to load PRD: %w", err)
	}

	summaryContent, err := r.loadSummary()
	if err != nil {
		return nil, fmt.Errorf("failed to load SUMMARY.md: %w", err)
	}

	// Build pre-fetched context
	contextContent, err := r.buildMediatedContext(whitelist)
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}

	// Build system prompt with pre-fetched context
	systemPrompt := r.buildSystemPromptMediatedMode(prdContent, summaryContent, contextContent, whitelist)

	// Count tokens
	systemTokens, _ := r.provider.CountTokens(systemPrompt)
	r.tokensUsed = systemTokens

	r.debug.Section("REVIEWER PHASE (Mediated) - Iteration %d", r.iteration)
	r.debug.Log("System prompt tokens: %d", systemTokens)
	r.debug.Log("Bail limit: %d tokens", r.bailLimit)

	// Check if already over bail limit
	if r.tokensUsed >= r.bailLimit {
		r.debug.Log("Context too large (%d tokens), using minimal context...", r.tokensUsed)
		// Use minimal context
		systemPrompt = r.buildMinimalSystemPrompt(prdContent, summaryContent)
		systemTokens, _ = r.provider.CountTokens(systemPrompt)
		r.tokensUsed = systemTokens
	}

	// Single request - no tool loop
	req := llm.Request{
		Model:     r.modelConfig.Name,
		System:    systemPrompt,
		Messages:  []llm.Message{},
		MaxTokens: 4096,
	}

	r.debug.Log("Sending mediated review request...")

	resp, err := r.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	r.tokensUsed += resp.Usage.TotalTokens

	r.debug.Log("Response received. Tokens used: %d (total: %d/%d)",
		resp.Usage.TotalTokens, r.tokensUsed, r.bailLimit)
	r.debug.TokenStatus(r.tokensUsed, r.bailLimit)

	// Write feedback file
	feedback := resp.Content
	if err := r.writeFeedback(ctx, feedback); err != nil {
		return nil, fmt.Errorf("failed to write feedback: %w", err)
	}

	return r.createResult(feedback), nil
}

// buildMediatedContext builds the pre-fetched context for mediated mode.
func (r *Reviewer) buildMediatedContext(whitelist config.Whitelist) (string, error) {
	var context strings.Builder

	// Get file tree
	fileTree, err := r.buildFileTree(whitelist)
	if err != nil {
		return "", err
	}

	context.WriteString("=== PROJECT FILE TREE ===\n")
	context.WriteString(fileTree)
	context.WriteString("\n=== END FILE TREE ===\n\n")

	// Get content of relevant files (up to token budget)
	context.WriteString("=== RELEVANT FILE CONTENTS ===\n")

	// Find Go files (prioritize these for code review) using glob tool
	goFiles, err := r.globFiles("**/*.go", whitelist)
	if err != nil {
		goFiles = []string{}
	}

	// Add files up to reasonable limit
	filesAdded := 0
	for _, path := range goFiles {
		if filesAdded >= 20 { // Limit to avoid token overflow
			context.WriteString(fmt.Sprintf("\n... and %d more files (not shown due to token limits)\n", len(goFiles)-filesAdded))
			break
		}

		data, err := r.sandbox.ReadFile(path)
		if err != nil {
			continue
		}

		// Skip very large files
		if len(data) > 10000 {
			context.WriteString(fmt.Sprintf("\n// File: %s (skipped - too large, %d bytes)\n", path, len(data)))
			continue
		}

		context.WriteString(fmt.Sprintf("\n// File: %s\n", path))
		context.WriteString(string(data))
		context.WriteString("\n")
		filesAdded++
	}

	context.WriteString("\n=== END FILE CONTENTS ===\n")

	return context.String(), nil
}

// globFiles uses the glob tool to find files matching a pattern.
func (r *Reviewer) globFiles(pattern string, whitelist config.Whitelist) ([]string, error) {
	args := map[string]any{"pattern": pattern}
	result, err := tools.Execute(context.Background(), "glob", args, whitelist)
	if err != nil || result.IsError {
		return nil, fmt.Errorf("glob failed: %v", err)
	}

	// Parse the result - it's a string with "Found N file(s):" header
	lines := strings.Split(result.Content, "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Found ") || line == "No files matched the pattern." {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// buildFileTree builds a tree representation of the project files.
func (r *Reviewer) buildFileTree(whitelist config.Whitelist) (string, error) {
	var tree strings.Builder

	// Find all files using glob tool
	allFiles, err := r.globFiles("**/*", whitelist)
	if err != nil {
		return "", err
	}

	for _, path := range allFiles {
		// Skip directories and hidden files
		if strings.HasPrefix(filepath.Base(path), ".") {
			continue
		}
		tree.WriteString(path + "\n")
	}

	return tree.String(), nil
}

// executeToolCall executes a single tool call and returns the result.
func (r *Reviewer) executeToolCall(ctx context.Context, tc llm.ToolCall, whitelist config.Whitelist) tools.Result {
	result, err := tools.Execute(ctx, tc.Name, tc.Arguments, whitelist)
	if err != nil {
		result = tools.Failure(err.Error())
	}

	r.debug.ToolExecution(tc.Name, tc.Arguments, result.Content, result.IsError)

	return result
}

// buildSystemPromptToolMode builds the system prompt for tool mode.
func (r *Reviewer) buildSystemPromptToolMode(prd, summary string, whitelist config.Whitelist) string {
	var prompt strings.Builder

	prompt.WriteString("You are Boberto Reviewer, a code review agent.\n\n")
	prompt.WriteString("Your task is to review the worker's implementation against the PRD:\n")
	prompt.WriteString("1. Read relevant files to understand what was implemented\n")
	prompt.WriteString("2. Compare implementation against PRD requirements\n")
	prompt.WriteString("3. Identify issues, bugs, missing features, or improvements needed\n")
	prompt.WriteString("4. If everything looks good, indicate completion (no feedback needed)\n\n")

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

	// Add worker's summary
	prompt.WriteString("=== WORKER SUMMARY ===\n")
	prompt.WriteString(summary)
	prompt.WriteString("\n=== END WORKER SUMMARY ===\n\n")

	prompt.WriteString(fmt.Sprintf("This is iteration %d. Review the implementation against the PRD. ", r.iteration))
	prompt.WriteString("Use the available tools to explore the codebase. ")
	prompt.WriteString("When you have completed your review, stop calling tools and provide your feedback. ")
	prompt.WriteString("If everything looks good and no changes are needed, respond with an empty message or just 'LGTM'. ")
	prompt.WriteString("Your final response will be saved as FEEDBACK.md.\n")

	return prompt.String()
}

// buildSystemPromptMediatedMode builds the system prompt for mediated mode.
func (r *Reviewer) buildSystemPromptMediatedMode(prd, summary, context string, whitelist config.Whitelist) string {
	var prompt strings.Builder

	prompt.WriteString("You are Boberto Reviewer, a code review agent.\n\n")
	prompt.WriteString("Your task is to review the worker's implementation against the PRD.\n\n")

	// Add PRD content
	prompt.WriteString("=== PRD ===\n")
	prompt.WriteString(prd)
	prompt.WriteString("\n=== END PRD ===\n\n")

	// Add worker's summary
	prompt.WriteString("=== WORKER SUMMARY ===\n")
	prompt.WriteString(summary)
	prompt.WriteString("\n=== END WORKER SUMMARY ===\n\n")

	// Add pre-fetched context
	prompt.WriteString(context)
	prompt.WriteString("\n")

	prompt.WriteString(fmt.Sprintf("This is iteration %d. Review the implementation against the PRD. ", r.iteration))
	prompt.WriteString("Compare what was implemented with what was required. ")
	prompt.WriteString("Identify any issues, bugs, missing features, or improvements needed. ")
	prompt.WriteString("If everything looks good and no changes are needed, respond with an empty message or just 'LGTM'. ")
	prompt.WriteString("Your response will be saved as FEEDBACK.md.\n")

	return prompt.String()
}

// buildMinimalSystemPrompt builds a minimal prompt when context is too large.
func (r *Reviewer) buildMinimalSystemPrompt(prd, summary string) string {
	var prompt strings.Builder

	prompt.WriteString("You are Boberto Reviewer, a code review agent.\n\n")
	prompt.WriteString("Due to token limits, you are receiving a minimal context.\n\n")

	// Add PRD content
	prompt.WriteString("=== PRD ===\n")
	prompt.WriteString(prd)
	prompt.WriteString("\n=== END PRD ===\n\n")

	// Add worker's summary
	prompt.WriteString("=== WORKER SUMMARY ===\n")
	prompt.WriteString(summary)
	prompt.WriteString("\n=== END WORKER SUMMARY ===\n\n")

	prompt.WriteString("Based on the PRD and worker summary, provide your review feedback. ")
	prompt.WriteString("If you need to inspect specific files, note them in your feedback. ")
	prompt.WriteString("If everything looks good, respond with an empty message or 'LGTM'.\n")

	return prompt.String()
}

// buildToolDefinitions returns tool definitions for the LLM.
func (r *Reviewer) buildToolDefinitions(whitelist config.Whitelist) []llm.ToolDefinition {
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
func (r *Reviewer) loadPRD() (string, error) {
	data, err := r.sandbox.ReadFile("PRD.md")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// loadSummary loads the SUMMARY.md file content.
func (r *Reviewer) loadSummary() (string, error) {
	data, err := r.sandbox.ReadFile("SUMMARY.md")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// writeFeedback writes the FEEDBACK.md file.
func (r *Reviewer) writeFeedback(ctx context.Context, content string) error {
	r.debug.Log("Writing FEEDBACK.md (%d bytes)", len(content))

	// If history mode is enabled, rotate the existing FEEDBACK.md file
	if r.history {
		if err := r.rotateFeedback(); err != nil {
			r.debug.Log("Warning: failed to rotate FEEDBACK.md: %v", err)
		}
	}

	data := []byte(content)
	return r.sandbox.WriteFile("FEEDBACK.md", data, 0644)
}

// rotateFeedback rotates the existing FEEDBACK.md to FEEDBACK_N.md.
func (r *Reviewer) rotateFeedback() error {
	// Check if FEEDBACK.md exists
	_, err := r.sandbox.Stat("FEEDBACK.md")
	if err != nil {
		// File doesn't exist, nothing to rotate
		return nil
	}

	// Find the next available iteration number
	iteration := 1
	for {
		historyPath := fmt.Sprintf("FEEDBACK_%d.md", iteration)
		_, err := r.sandbox.Stat(historyPath)
		if err != nil {
			// File doesn't exist, we can use this iteration number
			break
		}
		iteration++
	}

	// Read the existing FEEDBACK.md
	data, err := r.sandbox.ReadFile("FEEDBACK.md")
	if err != nil {
		return fmt.Errorf("failed to read FEEDBACK.md: %w", err)
	}

	// Write to the history file
	historyPath := fmt.Sprintf("FEEDBACK_%d.md", iteration)
	if err := r.sandbox.WriteFile(historyPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", historyPath, err)
	}

	r.debug.Log("Rotated FEEDBACK.md to %s", historyPath)
	return nil
}

// createResult creates a ReviewResult from feedback content.
func (r *Reviewer) createResult(feedback string) *ReviewResult {
	// Check if feedback is effectively empty (LGTM case)
	trimmed := strings.TrimSpace(feedback)
	isLGTM := trimmed == "" || strings.EqualFold(trimmed, "LGTM") ||
		strings.EqualFold(trimmed, "No feedback.") ||
		strings.EqualFold(trimmed, "No issues found.") ||
		strings.EqualFold(trimmed, "Looks good to me.") ||
		strings.EqualFold(trimmed, "Looks good to me!")

	return &ReviewResult{
		LGTM:     isLGTM,
		Feedback: feedback,
	}
}
