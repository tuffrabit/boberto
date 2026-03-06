# Boberto

Boberto is a Go-based CLI coding agent implementing the "ralph loop" pattern: a worker-reviewer iterative cycle for autonomous code generation and refinement.

## Overview

Boberto takes a Product Requirements Document (PRD.md) and autonomously implements it through an iterative loop:

1. **Worker Agent**: Reads the PRD, explores the codebase, writes code, and creates a summary of work done
2. **Reviewer Agent**: Reviews the work against the PRD, provides feedback
3. **Loop**: Continues until the reviewer approves (LGTM) or iteration limit reached

## Features

- **Local & Cloud LLM Support**: Works with LM Studio, Ollama, OpenAI, and Anthropic
- **Model Switching**: Automatically unloads worker model and loads reviewer model to manage VRAM (for local models)
- **Dual Reviewer Modes**: 
  - **Tool-calling mode**: Reviewer uses tools to explore codebase (for models with tool support)
  - **Mediated mode**: Boberto pre-fetches context, reviewer responds to structured prompt (for models without tool support)
- **Filesystem Sandbox**: All file operations constrained to project root with ignore patterns
- **Whitelist Enforcement**: Sensitive tools (bash, web fetch) require explicit allowlisting
- **Hot-reloadable Config**: Project config changes picked up each iteration
- **Token Management**: Tracks token usage and automatically bails to next iteration when approaching context limits
- **Debug Output**: Comprehensive logging with `-d` flag showing full LLM requests/responses and tool executions

## Installation

### Prerequisites

- Go 1.25.6 or later
- A PRD.md file in your project directory

### Build from Source

```bash
git clone <repository>
cd boberto
go build -o boberto ./cmd/boberto/main.go
```

Or install to `$GOPATH/bin`:

```bash
go install ./cmd/boberto
```

## Usage

```bash
boberto [options] <project-directory>
```

### Options

| Flag | Description |
|------|-------------|
| `-h, --help` | Show help message and exit |
| `-l, --limit N` | Maximum number of iterations (default: unlimited) |
| `-d, --debug` | Print detailed agent conversation to stdout |
| `--no-model-switch` | Disable model loading/unloading between phases |
| `--completion-mode` | When to consider work complete: `both` (default), `worker`, or `reviewer` |
| `--history` | Keep history of SUMMARY.md and FEEDBACK.md files as SUMMARY_N.md and FEEDBACK_N.md |

### Examples

```bash
# Run in current directory with default settings
./boberto

# Run with iteration limit and debug output
./boberto -l 5 -d /path/to/project

# Disable model switching (keep both models in VRAM)
./boberto --no-model-switch

# Stop when worker indicates completion (skip reviewer approval)
./boberto --completion-mode worker

# Stop when reviewer LGTMs (even if worker has more work)
./boberto --completion-mode reviewer

# Keep history of SUMMARY.md and FEEDBACK.md files
./boberto --history
```

## Configuration

### Global Config (`~/.boberto/config.json`)

Created automatically on first run with sensible defaults:

```json
{
  "models": {
    "qwen2.5-coder": {
      "api_type": "openai",
      "api_key": "not-needed",
      "uri": "http://localhost:1234/v1/chat/completions",
      "name": "qwen2.5-coder-14b",
      "local": true,
      "provider": "lmstudio",
      "context_window": 32768,
      "bail_threshold": 0.75,
      "supports_tool_calling": true
    },
    "llama3.3-reviewer": {
      "api_type": "openai",
      "api_key": "not-needed",
      "uri": "http://localhost:11434/v1/chat/completions",
      "name": "llama3.3",
      "local": true,
      "provider": "ollama",
      "context_window": 128000,
      "bail_threshold": 0.85,
      "supports_tool_calling": false
    },
    "gpt-4o": {
      "api_type": "openai",
      "api_key": "sk-...",
      "uri": "https://api.openai.com/v1/chat/completions",
      "name": "gpt-4o",
      "local": false,
      "context_window": 128000,
      "bail_threshold": 0.80,
      "supports_tool_calling": true
    },
    "claude-sonnet": {
      "api_type": "anthropic",
      "api_key": "sk-ant-...",
      "uri": "https://api.anthropic.com/v1/messages",
      "name": "claude-3-5-sonnet-20241022",
      "local": false,
      "context_window": 200000,
      "bail_threshold": 0.80,
      "supports_tool_calling": true
    }
  },
  "worker": {
    "default_model": "qwen2.5-coder"
  },
  "reviewer": {
    "default_model": "llama3.3-reviewer"
  }
}
```

**Model Configuration Fields:**
- `api_type`: `"openai"` or `"anthropic"`
- `api_key`: API key (use `"not-needed"` for local models)
- `uri`: Full API endpoint URL
- `name`: Model name as recognized by the provider
- `local`: `true` for local models (enables VRAM management)
- `provider`: `"lmstudio"`, `"ollama"`, or leave empty for standard OpenAI/Anthropic
- `context_window`: Maximum context size in tokens
- `bail_threshold`: Percentage of context window at which to bail (0.0-1.0)
- `supports_tool_calling`: Whether the model supports native tool calling
- `extra_body`: Additional JSON parameters to include in the API request body (provider-specific)

**Using `extra_body` for Model-Specific Parameters:**

Some models require special parameters in the request body. For example, to disable thinking mode in Qwen3.5 via LM Studio:

```json
{
  "models": {
    "qwen3.5": {
      "api_type": "openai",
      "api_key": "not-needed",
      "uri": "http://localhost:1234/v1/chat/completions",
      "name": "qwen3.5-35b",
      "local": true,
      "provider": "lmstudio",
      "context_window": 32768,
      "bail_threshold": 0.75,
      "supports_tool_calling": true,
      "extra_body": {
        "chat_template_kwargs": {"enable_thinking": false}
      }
    }
  }
}
```

### Project Config (`.boberto/config.json`)

Optional per-project configuration (hot-reloadable at the start of each iteration):

```json
{
  "ignore": [
    "node_modules/**",
    "*.log",
    ".git/**",
    "dist/**",
    "*.tmp"
  ],
  "whitelist": {
    "bash": ["go test ./...", "go build", "make"],
    "web_search": true,
    "web_fetch": ["https://api.github.com/**", "https://docs.rs/**"]
  }
}
```

## Project Structure

```
.
├── PRD.md              # Product Requirements Document (required)
├── SUMMARY.md          # Worker progress report (auto-generated each iteration)
├── FEEDBACK.md         # Reviewer feedback (auto-generated each iteration)
├── SUMMARY_1.md        # Previous iteration summary (with --history flag)
├── FEEDBACK_1.md       # Previous iteration feedback (with --history flag)
└── .boberto/
    └── config.json     # Project config (optional, hot-reloadable)
```

### History Mode

With the `--history` flag, Boberto preserves previous iterations' SUMMARY.md and FEEDBACK.md files by rotating them to numbered versions before writing new ones:

- Before writing SUMMARY.md, the existing file is renamed to SUMMARY_1.md, SUMMARY_2.md, etc.
- Before writing FEEDBACK.md, the existing file is renamed to FEEDBACK_1.md, FEEDBACK_2.md, etc.
- History files are automatically hidden from tool calls (glob, grep) so models remain unaware of them.
- When not using `--history`, the default behavior (overwriting files) remains unchanged.

## How It Works

### The Ralph Loop

```
┌─────────────────────────────────────────────────────────────────┐
│  START ITERATION N                                              │
│  1. Print: "Starting iteration N (last took ~Xms)"              │
│  2. Load project config (hot-reload)                            │
│  3. Load PRD.md, SUMMARY.md, FEEDBACK.md                        │
│  4. Model switching: unload reviewer, load worker (if local)    │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  WORKER PHASE                                                   │
│  1. Build system prompt with PRD, previous summary/feedback     │
│  2. Tool calling loop: read files, write files, execute tools   │
│  3. Track token usage, bail when approaching limit              │
│  4. Write SUMMARY.md when done                                  │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  REVIEWER PHASE                                                 │
│  1. Model switching: unload worker, load reviewer (if local)    │
│  2. If supports_tool_calling: explore codebase with tools       │
│  3. Else (mediated): Boberto pre-fetches context for reviewer   │
│  4. Write FEEDBACK.md (empty or "LGTM" if approved)             │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  CHECK COMPLETION                                               │
│  - If reviewer LGTM (and worker done in 'both' mode): exit      │
│  - Else: increment N, loop to START                             │
└─────────────────────────────────────────────────────────────────┘
```

### Available Tools

The worker and reviewer have access to these tools:

| Tool | Sensitive | Description |
|------|-----------|-------------|
| `read_file` | No | Read file contents with line limits |
| `write_file` | No | Write or append to files |
| `glob` | No | Find files matching pattern |
| `grep` | No | Search file contents with regex |
| `bash` | Yes | Execute shell commands (requires whitelist) |
| `web_fetch` | Yes | Fetch URL content (requires whitelist) |

### Token Management

Boberto tracks token usage throughout each iteration:
- Monitors cumulative tokens used (system prompt + conversation + tool results)
- When `used_tokens > (context_window * bail_threshold)`, the agent wraps up
- Worker writes SUMMARY.md before bailing
- Default bail threshold is 0.80 (80% of context window)
- Per-model configurable via `bail_threshold` in global config

### Debug Output

Use `-d` or `--debug` to see comprehensive logging:

```
[13:58:00.722] ═══════════════════════════════════════════════════════════════
[13:58:00.722] WORKER PHASE - Iteration 1
[13:58:00.722] ═══════════════════════════════════════════════════════════════
[13:58:00.722] System prompt tokens: 170
[13:58:00.722] Bail limit: 24576 tokens

[13:58:00.722] ═══════════════════════════════════════════════════════════════
[13:58:00.722] LLM REQUEST → qwen2.5-coder-14b
[13:58:00.722] ═══════════════════════════════════════════════════════════════
[13:58:00.722] SYSTEM PROMPT:
[13:58:00.722]   You are Boberto, a coding agent...
...
[13:58:00.722] TOOL CALLS (2):
[13:58:00.722]   [0] ID: call_abc123
[13:58:00.722]       Name: read_file
[13:58:00.722]       Arguments:
[13:58:00.722]         {"path": "main.go"}
...
[13:58:00.722] TOKEN USAGE:
[13:58:00.722]   Input tokens:  512
[13:58:00.722]   Output tokens: 128
[13:58:00.722]   Total tokens:  640
```

### Final Summary

On successful completion, Boberto prints a final summary:

```
═══════════════════════════════════════════════════════════════
                    FINAL SUMMARY
═══════════════════════════════════════════════════════════════
Total iterations: 5
Total time: 2m34s
Average iteration time: 30s

Iteration breakdown:
  Iteration 1: 45s
  Iteration 2: 28s
  Iteration 3: 31s
  Iteration 4: 22s
  Iteration 5: 18s
═══════════════════════════════════════════════════════════════
```

## Development

```bash
# Build
go build -o boberto ./cmd/boberto/main.go

# Run directly
go run ./cmd/boberto/main.go --help

# Test
go test ./...
```

## Architecture

```
boberto/
├── cmd/boberto/main.go       # CLI entry point
├── internal/
│   ├── agent/                # Worker and reviewer agents
│   │   ├── loop.go           # Ralph Loop orchestration
│   │   ├── worker.go         # Worker agent implementation
│   │   └── reviewer.go       # Reviewer agent implementation
│   ├── config/               # Configuration loading
│   │   ├── config.go         # Global config
│   │   └── project.go        # Project config
│   ├── debug/                # Debug logging
│   │   └── logger.go         # Comprehensive debug output
│   ├── fs/                   # Filesystem sandbox
│   │   ├── sandbox.go        # Path validation and sandboxing
│   │   └── gitignore.go      # Ignore pattern matching
│   ├── llm/                  # LLM providers
│   │   ├── client.go         # Provider interface
│   │   ├── openai.go         # OpenAI-compatible provider
│   │   ├── anthropic.go      # Anthropic provider
│   │   ├── lmstudio.go       # LM Studio provider
│   │   ├── ollama.go         # Ollama provider
│   │   ├── factory.go        # Provider factory
│   │   └── tokenizer.go      # Token counting
│   └── tools/                # Tool system
│       ├── registry.go       # Tool registration
│       ├── tool.go           # Tool interface
│       ├── readfile.go       # Read file tool
│       ├── writefile.go      # Write file tool
│       ├── glob.go           # Glob pattern tool
│       ├── grep.go           # Grep search tool
│       ├── bash.go           # Shell execution tool
│       └── webfetch.go       # Web fetch tool
```

## License

[LICENSE](LICENSE)
