# Boberto

Boberto is a Go-based CLI coding agent implementing the "ralph loop" pattern: a worker-reviewer iterative cycle for autonomous code generation and refinement.

## Overview

Boberto takes a Product Requirements Document (PRD.md) and autonomously implements it through an iterative loop:

1. **Worker Agent**: Reads the PRD, writes code, creates a summary of work done
2. **Reviewer Agent**: Reviews the work, provides feedback
3. **Loop**: Continues until the reviewer is satisfied or iteration limit reached

## Features

- **Local & Cloud LLM Support**: Works with LM Studio, Ollama, OpenAI, and Anthropic
- **Model Switching**: Automatically unloads worker model and loads reviewer model to manage VRAM
- **Filesystem Sandbox**: All file operations constrained to project root with ignore patterns
- **Whitelist Enforcement**: Sensitive tools (bash, web search) require explicit allowlisting
- **Hot-reloadable Config**: Project config changes picked up each iteration

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
| `-d, --debug` | Print agent conversation to stdout |
| `--no-model-switch` | Disable model loading/unloading between phases |
| `--completion-mode` | When to consider work complete: `both` (default), `worker`, or `reviewer` |

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
      "bail_threshold": 0.75
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

### Project Config (`.boberto/config.json`)

Optional per-project configuration (hot-reloadable):

```json
{
  "ignore": [
    "node_modules/**",
    "*.log",
    ".git/**"
  ],
  "whitelist": {
    "bash": ["go test ./...", "go build"],
    "web_search": true,
    "web_fetch": ["https://api.github.com/**"]
  }
}
```

## Project Structure

```
.
├── PRD.md              # Product Requirements Document (required)
├── SUMMARY.md          # Worker progress report (auto-generated)
├── FEEDBACK.md         # Reviewer feedback (auto-generated)
└── .boberto/
    └── config.json     # Project config (optional)
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

## License

[LICENSE](LICENSE)
