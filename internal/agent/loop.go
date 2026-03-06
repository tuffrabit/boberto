// Package agent provides the worker and reviewer agent implementations.
package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tuffrabit/boberto/internal/config"
	"github.com/tuffrabit/boberto/internal/debug"
	"github.com/tuffrabit/boberto/internal/fs"
	"github.com/tuffrabit/boberto/internal/llm"
)

// CompletionMode defines how the loop determines when work is complete.
type CompletionMode string

const (
	// CompletionModeBoth requires both worker and reviewer to indicate completion (default).
	CompletionModeBoth CompletionMode = "both"
	// CompletionModeWorker only requires the worker to indicate completion.
	CompletionModeWorker CompletionMode = "worker"
	// CompletionModeReviewer only requires the reviewer to indicate completion.
	CompletionModeReviewer CompletionMode = "reviewer"
)

// LoopOptions contains configuration options for the Ralph Loop.
type LoopOptions struct {
	// GlobalConfig is the loaded global configuration.
	GlobalConfig *config.Global

	// ProjectDir is the path to the project directory.
	ProjectDir string

	// Sandbox is the filesystem sandbox.
	Sandbox *fs.Sandbox

	// Limit is the maximum number of iterations (0 = unlimited).
	Limit int

	// Debug enables debug output.
	Debug bool

	// NoModelSwitch disables model loading/unloading between phases.
	NoModelSwitch bool

	// CompletionMode determines when the loop considers work complete.
	CompletionMode CompletionMode

	// History enables keeping history of SUMMARY.md and FEEDBACK.md files.
	History bool
}

// IterationStats tracks statistics for a single iteration.
type IterationStats struct {
	Number      int
	Duration    time.Duration
	WorkerTokens int
	ReviewerTokens int
}

// Loop represents the Ralph Loop orchestrator that manages worker-reviewer iterations.
type Loop struct {
	opts        LoopOptions
	iteration   int
	lastIterTime time.Duration
	debug       *debug.Logger

	// Model state tracking
	workerModelLoaded   bool
	reviewerModelLoaded bool
	
	// Statistics tracking
	iterations []IterationStats
	startTime  time.Time
	
	// History tracking
	historyRotated bool
}

// NewLoop creates a new Ralph Loop orchestrator.
func NewLoop(opts LoopOptions) *Loop {
	return &Loop{
		opts:                opts,
		iteration:           0,
		lastIterTime:        0,
		debug:               debug.NewLogger(opts.Debug),
		workerModelLoaded:   false,
		reviewerModelLoaded: false,
		iterations:          make([]IterationStats, 0),
		startTime:           time.Now(),
		historyRotated:      false,
	}
}

// Run executes the Ralph Loop until completion or limit reached.
func (l *Loop) Run(ctx context.Context) error {
	for {
		l.iteration++

		// Check iteration limit
		if l.opts.Limit > 0 && l.iteration > l.opts.Limit {
			return fmt.Errorf("iteration limit (%d) reached", l.opts.Limit)
		}

		startTime := time.Now()

		// Print iteration start
		fmt.Printf("\nStarting iteration %d", l.iteration)
		if l.iteration > 1 {
			fmt.Printf(" (last took ~%dms)", l.lastIterTime.Milliseconds())
		}
		fmt.Println()

		// Rotate history files at the start of each new iteration (after the first)
		// This ensures the models don't see the previous iteration's files
		if l.opts.History && l.iteration > 1 && !l.historyRotated {
			if err := l.rotateHistoryFiles(); err != nil {
				fmt.Printf("Warning: failed to rotate history files: %v\n", err)
			}
			l.historyRotated = true
		}

		// Hot-reload project config at start of each iteration
		projectCfg, err := config.LoadProject(l.opts.ProjectDir)
		if err != nil {
			// Log warning but continue with previous config
			fmt.Printf("Warning: failed to reload project config: %v\n", err)
		}

		// Get model configs
		workerModelCfg, err := l.opts.GlobalConfig.GetModel(l.opts.GlobalConfig.Worker.DefaultModel)
		if err != nil {
			return fmt.Errorf("failed to get worker model config: %w", err)
		}

		reviewerModelCfg, err := l.opts.GlobalConfig.GetModel(l.opts.GlobalConfig.Reviewer.DefaultModel)
		if err != nil {
			return fmt.Errorf("failed to get reviewer model config: %w", err)
		}

		// Determine if both models are local (for model switching)
		bothLocal := workerModelCfg.Local && reviewerModelCfg.Local

		// ========== WORKER PHASE ==========

		// Model switching: check previous phase model status, unload if needed, load worker
		if !l.opts.NoModelSwitch && bothLocal {
			// Check if a different model is already loaded from a previous run
			loadedModel, err := l.getCurrentlyLoadedModel(ctx, workerModelCfg)
			if err != nil {
				fmt.Printf("Warning: failed to check loaded model: %v\n", err)
			}
			
			// If reviewer model is loaded (from previous interrupted run), unload it
			if loadedModel != "" && loadedModel != workerModelCfg.Name {
				fmt.Printf("Unloading previously loaded model: %s\n", loadedModel)
				if err := l.unloadModel(ctx, reviewerModelCfg); err != nil {
					fmt.Printf("Warning: failed to unload previously loaded model: %v\n", err)
				}
				l.reviewerModelLoaded = false
			} else if l.reviewerModelLoaded {
				// Normal case: reviewer was loaded in this session
				if err := l.unloadModel(ctx, reviewerModelCfg); err != nil {
					fmt.Printf("Warning: failed to unload reviewer model: %v\n", err)
				}
				l.reviewerModelLoaded = false
			}
			
			if err := l.loadModel(ctx, workerModelCfg); err != nil {
				return fmt.Errorf("failed to load worker model after retries: %w", err)
			}
			l.workerModelLoaded = true
		}

		// Run worker with cascading retry logic
		workerDone, err := l.runWorkerWithRetry(ctx, workerModelCfg, projectCfg.Whitelist)
		if err != nil {
			return fmt.Errorf("worker failed after retries: %w", err)
		}

		// Check completion (worker-only mode)
		if l.opts.CompletionMode == CompletionModeWorker && workerDone {
			fmt.Println("\nWorker indicates task is complete.")
			l.printFinalSummary()
			l.cleanup(ctx, reviewerModelCfg, bothLocal)
			return nil
		}

		// ========== REVIEWER PHASE ==========

		// Model switching: check previous phase model status, unload if needed, load reviewer
		if !l.opts.NoModelSwitch && bothLocal {
			// Check if a different model is already loaded from a previous run
			loadedModel, err := l.getCurrentlyLoadedModel(ctx, reviewerModelCfg)
			if err != nil {
				fmt.Printf("Warning: failed to check loaded model: %v\n", err)
			}
			
			// If worker model is loaded (from previous interrupted run), unload it
			if loadedModel != "" && loadedModel != reviewerModelCfg.Name {
				fmt.Printf("Unloading previously loaded model: %s\n", loadedModel)
				if err := l.unloadModel(ctx, workerModelCfg); err != nil {
					fmt.Printf("Warning: failed to unload previously loaded model: %v\n", err)
				}
				l.workerModelLoaded = false
			} else if l.workerModelLoaded {
				// Normal case: worker was loaded in this session
				if err := l.unloadModel(ctx, workerModelCfg); err != nil {
					fmt.Printf("Warning: failed to unload worker model: %v\n", err)
				}
				l.workerModelLoaded = false
			}
			
			if err := l.loadModel(ctx, reviewerModelCfg); err != nil {
				return fmt.Errorf("failed to load reviewer model after retries: %w", err)
			}
			l.reviewerModelLoaded = true
		}

		// Run reviewer with cascading retry logic
		reviewResult, err := l.runReviewerWithRetry(ctx, reviewerModelCfg, projectCfg.Whitelist)
		if err != nil {
			return fmt.Errorf("reviewer failed after retries: %w", err)
		}

		// Check completion
		switch l.opts.CompletionMode {
		case CompletionModeReviewer:
			if reviewResult.LGTM {
				fmt.Println("\nReviewer indicates task is complete (LGTM).")
				l.printFinalSummary()
				l.cleanup(ctx, reviewerModelCfg, bothLocal)
				return nil
			}
		case CompletionModeBoth:
			if workerDone && reviewResult.LGTM {
				fmt.Println("\nBoth worker and reviewer indicate task is complete.")
				l.printFinalSummary()
				l.cleanup(ctx, reviewerModelCfg, bothLocal)
				return nil
			}
		}

		// Update timing and track stats
		l.lastIterTime = time.Since(startTime)
		l.iterations = append(l.iterations, IterationStats{
			Number:   l.iteration,
			Duration: l.lastIterTime,
		})
		fmt.Printf("\nIteration %d completed in %dms\n", l.iteration, l.lastIterTime.Milliseconds())

		// Reset history rotation flag for next iteration
		l.historyRotated = false

		// If worker is done but reviewer isn't (in both mode), we continue looping
		// The worker will see the reviewer's feedback and may have more work to do
	}
}

// printFinalSummary prints the final statistics summary.
func (l *Loop) printFinalSummary() {
	totalDuration := time.Since(l.startTime)
	
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("                    FINAL SUMMARY")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("Total iterations: %d\n", len(l.iterations))
	fmt.Printf("Total time: %s\n", totalDuration.Round(time.Second))
	
	if len(l.iterations) > 0 {
		// Calculate average iteration time
		var totalIterTime time.Duration
		for _, stats := range l.iterations {
			totalIterTime += stats.Duration
		}
		avgIterTime := totalIterTime / time.Duration(len(l.iterations))
		fmt.Printf("Average iteration time: %s\n", avgIterTime.Round(time.Millisecond))
		
		// Show breakdown
		fmt.Println("\nIteration breakdown:")
		for _, stats := range l.iterations {
			fmt.Printf("  Iteration %d: %s\n", stats.Number, stats.Duration.Round(time.Millisecond))
		}
	}
	
	fmt.Println("═══════════════════════════════════════════════════════════════")
}

// runWorkerWithRetry runs the worker with cascading retry logic:
// 1. Retry tool calls (handled within worker)
// 2. If iteration fails, retry the entire iteration once
// 3. If iteration retry fails, return error
func (l *Loop) runWorkerWithRetry(ctx context.Context, modelCfg config.ModelConfig, whitelist config.Whitelist) (bool, error) {
	provider, err := llm.NewProviderFromConfigWithDebug(modelCfg, l.debug)
	if err != nil {
		return false, fmt.Errorf("failed to create worker provider: %w", err)
	}

	// First attempt
	done, err := l.runWorker(ctx, provider, modelCfg, whitelist)
	if err == nil {
		return done, nil
	}

	// If error is not a bail (token limit), retry the iteration once
	if !isBailError(err) {
		fmt.Printf("Worker error: %v. Retrying iteration with bail...\n", err)
		
		// Retry iteration with bail
		done, retryErr := l.runWorker(ctx, provider, modelCfg, whitelist)
		if retryErr == nil {
			return done, nil
		}
		
		return false, fmt.Errorf("worker failed after retry: %w (original: %v)", retryErr, err)
	}

	// This was a bail (token limit), which is expected behavior
	return false, nil
}

// runWorker executes a single worker iteration.
func (l *Loop) runWorker(ctx context.Context, provider llm.Provider, modelCfg config.ModelConfig, whitelist config.Whitelist) (bool, error) {
	worker := NewWorker(WorkerOptions{
		Provider:    provider,
		ModelConfig: modelCfg,
		Sandbox:     l.opts.Sandbox,
		ProjectDir:  l.opts.ProjectDir,
		Debug:       l.debug,
		Iteration:   l.iteration,
	})

	return worker.Run(ctx)
}

// runReviewerWithRetry runs the reviewer with cascading retry logic.
func (l *Loop) runReviewerWithRetry(ctx context.Context, modelCfg config.ModelConfig, whitelist config.Whitelist) (*ReviewResult, error) {
	provider, err := llm.NewProviderFromConfigWithDebug(modelCfg, l.debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create reviewer provider: %w", err)
	}

	// First attempt
	result, err := l.runReviewer(ctx, provider, modelCfg, whitelist)
	if err == nil {
		return result, nil
	}

	// If error is not a bail (token limit), retry the iteration once
	if !isBailError(err) {
		fmt.Printf("Reviewer error: %v. Retrying iteration with bail...\n", err)
		
		// Retry iteration
		result, retryErr := l.runReviewer(ctx, provider, modelCfg, whitelist)
		if retryErr == nil {
			return result, nil
		}
		
		return nil, fmt.Errorf("reviewer failed after retry: %w (original: %v)", retryErr, err)
	}

	// This was a bail (token limit) - return empty feedback to continue loop
	return &ReviewResult{LGTM: false, Feedback: ""}, nil
}

// runReviewer executes a single reviewer iteration.
func (l *Loop) runReviewer(ctx context.Context, provider llm.Provider, modelCfg config.ModelConfig, whitelist config.Whitelist) (*ReviewResult, error) {
	reviewer := NewReviewer(ReviewerOptions{
		Provider:    provider,
		ModelConfig: modelCfg,
		Sandbox:     l.opts.Sandbox,
		ProjectDir:  l.opts.ProjectDir,
		Debug:       l.debug,
		Iteration:   l.iteration,
	})

	return reviewer.Run(ctx)
}

// loadModel loads a model via the provider.
func (l *Loop) loadModel(ctx context.Context, modelCfg config.ModelConfig) error {
	provider, err := llm.NewProviderFromConfigWithDebug(modelCfg, l.debug)
	if err != nil {
		return err
	}

	if !provider.SupportsModelManagement() {
		return nil
	}

	l.debug.Log("Loading model: %s", modelCfg.Name)

	return provider.LoadModel(ctx, modelCfg.Name)
}

// unloadModel unloads a model via the provider.
func (l *Loop) unloadModel(ctx context.Context, modelCfg config.ModelConfig) error {
	provider, err := llm.NewProviderFromConfigWithDebug(modelCfg, l.debug)
	if err != nil {
		return err
	}

	if !provider.SupportsModelManagement() {
		return nil
	}

	l.debug.Log("Unloading model: %s", modelCfg.Name)

	return provider.UnloadModel(ctx, modelCfg.Name)
}

// getCurrentlyLoadedModel checks which model is currently loaded in the provider.
// Returns empty string if no model is loaded or if the provider doesn't support model management.
func (l *Loop) getCurrentlyLoadedModel(ctx context.Context, modelCfg config.ModelConfig) (string, error) {
	provider, err := llm.NewProviderFromConfigWithDebug(modelCfg, l.debug)
	if err != nil {
		return "", err
	}

	if !provider.SupportsModelManagement() {
		return "", nil
	}

	loadedModel, err := provider.GetLoadedModel(ctx)
	if err != nil {
		return "", err
	}

	if loadedModel != "" {
		l.debug.Log("Provider reports loaded model: %s", loadedModel)
	}

	return loadedModel, nil
}

// cleanup performs cleanup at loop exit.
func (l *Loop) cleanup(ctx context.Context, reviewerModelCfg config.ModelConfig, bothLocal bool) {
	if !l.opts.NoModelSwitch && bothLocal && l.reviewerModelLoaded {
		if err := l.unloadModel(ctx, reviewerModelCfg); err != nil {
			fmt.Printf("Warning: failed to unload reviewer model on exit: %v\n", err)
		}
	}
}

// isBailError checks if an error represents a token limit bail.
// In the current implementation, bails are handled internally by the worker/reviewer
// and don't return errors. This function can be extended if we add explicit bail errors.
func isBailError(err error) bool {
	if err == nil {
		return false
	}
	// For now, we don't have explicit bail errors
	// The worker/reviewer handle bails internally and return nil error
	return false
}

// rotateHistoryFiles rotates SUMMARY.md and FEEDBACK.md to their history versions.
// This should be called at the start of each iteration (after the first).
func (l *Loop) rotateHistoryFiles() error {
	l.debug.Log("Rotating history files for iteration %d", l.iteration)

	// Rotate SUMMARY.md
	if err := l.rotateFile("SUMMARY.md"); err != nil {
		return fmt.Errorf("failed to rotate SUMMARY.md: %w", err)
	}

	// Rotate FEEDBACK.md
	if err := l.rotateFile("FEEDBACK.md"); err != nil {
		return fmt.Errorf("failed to rotate FEEDBACK.md: %w", err)
	}

	return nil
}

// rotateFile rotates a single file to its history version (e.g., FILE.md -> FILE_N.md).
func (l *Loop) rotateFile(filename string) error {
	// Check if file exists
	_, err := l.opts.Sandbox.Stat(filename)
	if err != nil {
		// File doesn't exist, nothing to rotate
		return nil
	}

	// Find the next available iteration number
	iteration := 1
	for {
		historyPath := fmt.Sprintf("%s_%d.md", filename[:len(filename)-3], iteration)
		_, err := l.opts.Sandbox.Stat(historyPath)
		if err != nil {
			// File doesn't exist, we can use this iteration number
			break
		}
		iteration++
	}

	// Read the existing file
	data, err := l.opts.Sandbox.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filename, err)
	}

	// Write to the history file
	historyPath := fmt.Sprintf("%s_%d.md", filename[:len(filename)-3], iteration)
	if err := l.opts.Sandbox.WriteFile(historyPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", historyPath, err)
	}

	// Delete the original file so the model sees no existing file
	validPath, err := l.opts.Sandbox.Validate(filename)
	if err != nil {
		return fmt.Errorf("failed to validate %s for deletion: %w", filename, err)
	}
	if err := os.Remove(validPath); err != nil {
		return fmt.Errorf("failed to remove %s: %w", filename, err)
	}

	l.debug.Log("Rotated %s to %s", filename, historyPath)
	fmt.Printf("  Rotated %s -> %s\n", filename, historyPath)
	return nil
}
