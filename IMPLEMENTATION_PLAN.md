# Boberto Implementation Plan

## Overview

Boberto is a Go-based CLI coding agent implementing the "ralph loop" pattern: a worker-reviewer iterative cycle for autonomous code generation and refinement.

## Core Architecture

### Directory Structure

```
boberto/
├── cmd/
│   └── boberto/
│       └── main.go              # CLI entry point
├── internal/
│   ├── agent/
│   │   ├── agent.go             # Core agent orchestration
│   │   ├── worker.go            # Worker agent implementation
│   │   └── reviewer.go          # Reviewer agent implementation
│   ├── config/
│   │   ├── config.go            # Config structures and global loading
│   │   └── project.go           # Project config loading (per-iteration)
│   ├── llm/
│   │   ├── client.go            # Raw HTTP LLM client interface
│   │   ├── openai.go            # OpenAI-compatible provider
│   │   ├── anthropic.go         # Anthropic provider
│   │   └── tokenizer.go         # Token counting utilities
│   ├── tools/
│   │   ├── registry.go          # Tool registration and dispatch
│   │   ├── tool.go              # Tool interface and base types
│   │   ├── readfile.go          # Read file tool
│   │   ├── writefile.go         # Write file tool
│   │   ├── glob.go              # Glob pattern tool
│   │   ├── grep.go              # Grep search tool
│   │   ├── bash.go              # Shell execution tool (sensitive)
│   │   ├── websearch.go         # Web search tool (sensitive)
│   │   └── webfetch.go          # Web fetch tool (sensitive)
│   ├── fs/
│   │   ├── sandbox.go           # Filesystem sandbox enforcement
│   │   └── gitignore.go         # Ignore pattern matching
│   └── prd/
│       └── prd.go               # PRD reading utilities
├── go.mod
└── README.md
```

## Configuration System

### Global Config (`~/.boberto/config.json`)

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
    },
    "llama3.3-reviewer": {
      "api_type": "openai", 
      "api_key": "not-needed",
      "uri": "http://localhost:11434/v1/chat/completions",
      "name": "llama3.3",
      "local": true,
      "provider": "ollama",
      "context_window": 128000,
      "bail_threshold": 0.85
    },
    "gpt-4o": {
      "api_type": "openai",
      "api_key": "sk-...",
      "uri": "https://api.openai.com/v1/chat/completions",
      "name": "gpt-4o",
      "local": false,
      "context_window": 128000,
      "bail_threshold": 0.80
    },
    "claude-sonnet": {
      "api_type": "anthropic",
      "api_key": "sk-ant-...",
      "uri": "https://api.anthropic.com/v1/messages",
      "name": "claude-3-5-sonnet-20241022",
      "local": false,
      "context_window": 200000,
      "bail_threshold": 0.80
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

Loaded at the start of **each iteration** (hot-reloadable):

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
    "web_fetch": ["https://api.github.com/**"]
  }
}
```

## Agent Loop (Ralph Loop)

### Iteration Flow

```
┌─────────────────────────────────────────────────────────────────┐
│  START ITERATION N                                              │
│  1. Print: "Starting iteration N (last took ~Xms)"              │
│  2. Load project config (hot-reload)                            │
│  3. Load PRD.md, SUMMARY.md (if exists), FEEDBACK.md (if exists)│
│  4. If model switching enabled AND both models local:           │
│     - Unload reviewer model (if loaded from prev iteration)     │
│     - Load worker model                                         │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  WORKER PHASE                                                   │
│  1. Build system prompt with:                                   │
│     - Available tools (respecting whitelist)                    │
│     - PRD content                                               │
│     - Previous SUMMARY.md (if N > 1)                            │
│     - Previous FEEDBACK.md (if N > 1)                           │
│     - Current token usage context                               │
│  2. While token budget available:                               │
│     - Send request to LLM                                       │
│     - Process tool calls until done signal                      │
│     - Track token usage after each exchange                     │
│     - If approaching limit: break and write SUMMARY.md          │
│  3. Write SUMMARY.md (human-readable progress report)           │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  WORKER→REVIEWER TRANSITION (if model switching enabled)        │
│  1. If both models are local:                                   │
│     - Unload worker model                                       │
│     - Load reviewer model                                       │
│  2. (Skip if: models are cloud, or mixed local/cloud)           │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  REVIEWER PHASE (if reviewer enabled)                           │
│  1. Build reviewer prompt with:                                 │
│     - PRD content                                               │
│     - Worker's SUMMARY.md                                       │
│     - List of changed files                                     │
│  2. Request review from LLM                                     │
│  3. If token limit approached during review:                    │
│     - Bail and write abbreviated FEEDBACK.md                    │
│  4. Write FEEDBACK.md (findings and suggestions)                │
│  5. If FEEDBACK.md indicates "no feedback" / LGTM:              │
│     - Exit loop, mark as complete                               │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  CHECK ITERATION LIMIT                                          │
│  - If -l/--limit reached: exit with warning                     │
│  - Else: increment N, loop to START                             │
└─────────────────────────────────────────────────────────────────┘
```

## Tool System

### Tool Interface

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() jsonschema.Schema  // JSON Schema for validation
    Execute(ctx context.Context, args map[string]any, whitelist Whitelist) (Result, error)
    IsSensitive() bool              // Requires whitelist check
}

type Result struct {
    Content string
    Error   error
}

type Whitelist struct {
    Bash       []string // Allowed bash commands (patterns)
    WebSearch  bool     // Whether web search is enabled
    WebFetch   []string // Allowed URL patterns
}
```
```

### Built-in Tools

| Tool | Sensitive | Description |
|------|-----------|-------------|
| `read_file` | No | Read file contents with line limits |
| `write_file` | No | Write or append to files |
| `glob` | No | Find files matching pattern |
| `grep` | No | Search file contents with regex |
| `bash` | Yes | Execute shell commands |
| `web_search` | Yes | Search the web |
| `web_fetch` | Yes | Fetch URL content |

### Tool Registry

- Tools register themselves via `init()` functions
- Registry provides available tools to agent
- Tool failures return error to LLM for retry

### Whitelist Enforcement

**Critical**: Whitelist checking happens **inside the tool's Execute method**, not in the registry.

```go
func (t *BashTool) Execute(ctx context.Context, args map[string]any, whitelist Whitelist) (Result, error) {
    command := args["command"].(string)
    
    // Tool ITSELF checks whitelist
    if !whitelist.AllowsBash(command) {
        return Result{}, fmt.Errorf("command not in whitelist: %s", command)
    }
    
    // Execute...
}
```

**Why this design?**
- LLM may craft tool calls that bypass registry filtering
- Tool receives raw arguments from LLM response
- Tool must validate against whitelist before acting
- Each sensitive tool implements its own whitelist logic:
  - `bash`: Checks command against allowed patterns
  - `web_search`: Checks if search is enabled
  - `web_fetch`: Checks URL against allowed patterns

**Whitelist Config Structure** (from project config):

```json
{
  "whitelist": {
    "bash": ["go test ./...", "go build", "make build", "npm run build"],
    "web_search": true,
    "web_fetch": ["https://api.github.com/**", "https://docs.rs/**"]
  }
}
```

## LLM Client Design

### Provider Interface

```go
type Provider interface {
    Complete(ctx context.Context, req Request) (Response, error)
    CountTokens(text string) int
    LoadModel(ctx context.Context, modelName string) error
    UnloadModel(ctx context.Context, modelName string) error
    SupportsModelManagement() bool  // Returns true for local providers
}

type Request struct {
    Model       string
    System      string
    Messages    []Message
    Tools       []ToolDefinition
    MaxTokens   int
}

type Response struct {
    Content   string
    ToolCalls []ToolCall
    Usage     TokenUsage
    Done      bool
}
```

### Provider Implementations

**OpenAI-compatible**: Raw HTTP to `/v1/chat/completions`
- Supports tool calling via `tools`/`tool_calls`
- `SupportsModelManagement()` returns `false`

**Anthropic**: Raw HTTP to `/v1/messages`
- Native tool use via `tools`/`tool_use`/`tool_result`
- `SupportsModelManagement()` returns `false`

**LM Studio** (`provider: "lmstudio"`):
- OpenAI-compatible API at custom URI
- Model management via `/v1/models/load` and unload endpoints
- `SupportsModelManagement()` returns `true`

**Ollama** (`provider: "ollama"`):
- OpenAI-compatible API at `/v1/chat/completions`
- Model management via `/api/generate` with `"keep_alive": 0` to unload
- `SupportsModelManagement()` returns `true`

All use standard library `net/http` - no external SDK dependencies.

## Filesystem Sandbox

### Safety Rules

1. **Project Boundary**: All file operations constrained to project root
2. **Symlink Resolution**: Resolve and validate symlinks before access
3. **Path Traversal**: Reject paths containing `..` or absolute paths outside root
4. **Ignore Patterns**: Apply `.boberto/config.json` ignore patterns to all operations

### Sandbox Implementation

```go
type Sandbox struct {
    Root   string
    Ignore *Gitignore
}

func (s *Sandbox) Validate(path string) error {
    // 1. Resolve to absolute path
    // 2. Ensure within Root
    // 3. Check against ignore patterns
}
```

## CLI Interface

```
Usage: boberto [options] <project-directory>

Options:
  -h, --help            Show this help message and exit
  -l, --limit N         Maximum number of iterations (default: unlimited)
  -d, --debug           Print agent conversation to stdout
  --no-model-switch     Disable model loading/unloading between phases
                        (default: model switching enabled)

Arguments:
  project-directory   Path to project root containing PRD.md
                      (default: current directory)

Configuration:
  Global config:   ~/.boberto/config.json
  Project config:  <project-directory>/.boberto/config.json

Expected Files:
  PRD.md          Product Requirements Document (required)
  SUMMARY.md      Written by worker each iteration
  FEEDBACK.md     Written by reviewer each iteration
```

## Token Management

### Context Window Strategy

1. **Pre-flight**: Count tokens in system prompt + PRD + previous files
2. **Reserve**: Keep buffer for response + tool results (based on `1 - bail_threshold`)
3. **Monitor**: Track cumulative usage during conversation
4. **Bail condition**: When `used_tokens > (context_window * bail_threshold)`
5. **Action**: Write summary/feedback and start next iteration

### Per-Model Bail Threshold

Each model can configure its own bail threshold in the global config:

```json
{
  "models": {
    "my-model": {
      "bail_threshold": 0.75
    }
  }
}
```

- **Default**: `0.80` (80% of context window)
- **Range**: `0.0` - `1.0` (values outside this range clamped)
- **Use case**: Adjust based on model behavior:
  - Lower threshold (0.75): For models that underestimate token counts
  - Higher threshold (0.90): For models with accurate counting, maximizing context usage

The agent calculates: `bail_limit = context_window * bail_threshold`

### Token Counting

- Use provider-native tokenizers where available
- Fallback to approximate counting (4 chars ≈ 1 token)
- Over-estimate to be safe

## Error Handling

| Error Type | Behavior |
|------------|----------|
| API Rate Limit | Print error, exit immediately |
| API Auth Error | Print error, exit immediately |
| Tool Execution Error | Return to LLM as tool result |
| Sandbox Violation | Log warning, reject operation, return error |
| Config Parse Error | Log error, use defaults, continue |
| PRD Not Found | Print error, exit with usage |

## Model Switching (VRAM Management)

### Purpose

For local LLM usage, GPU VRAM is often the limiting factor. This feature manages VRAM by unloading the current model before loading the next one, allowing users to run different models for worker and reviewer without requiring VRAM for both simultaneously.

### When It Applies

| Worker | Reviewer | Behavior |
|--------|----------|----------|
| Local | Local | **Switch models** - Unload one, load other |
| Local | Cloud | Keep both loaded (no switching) |
| Cloud | Local | Keep both loaded (no switching) |
| Cloud | Cloud | Keep both loaded (no switching) |

### CLI Flag

`--no-model-switch`: Disables model loading/unloading. Useful if:
- You have plenty of VRAM and want faster transitions
- You're debugging model loading issues

### Model State Tracking

```go
type ModelState struct {
    CurrentModel  string
    IsLoaded      bool
    Provider      Provider
}
```

### Switching Flow

```go
// Before worker phase
if modelSwitchingEnabled && bothModelsLocal {
    if reviewerModelIsLoaded {
        provider.UnloadModel(reviewerModel)
    }
    provider.LoadModel(workerModel)
    workerModelIsLoaded = true
}

// After worker, before reviewer
if modelSwitchingEnabled && bothModelsLocal {
    provider.UnloadModel(workerModel)
    provider.LoadModel(reviewerModel)
    reviewerModelIsLoaded = true
}

// On successful completion
if modelSwitchingEnabled && reviewerModelIsLoaded {
    provider.UnloadModel(reviewerModel)
}
```

### Provider-Specific Implementation

**LM Studio**:
```
POST /v1/models/load
{ "model": "model-name" }

POST /v1/models/unload
{ "model": "model-name" }
```

**Ollama**:
- Loading: Standard generate request with `keep_alive: -1` (or long duration)
- Unloading: Generate request with `keep_alive: 0`

### Error Handling

- Model load/unload failures are **non-fatal warnings**
- Agent continues with best effort (model may already be loaded)
- Printed to stdout: `Warning: failed to unload worker model: <error>`

## Extensibility Design

### Future Agent Patterns

The codebase should easily accommodate:

1. **Planner-Executor**: Add planner agent that breaks PRD into tasks
2. **Multi-Agent**: Add specialized agents (tester, docs, security)
3. **Human-in-Loop**: Insert approval gates between phases
4. **Streaming**: Real-time tool execution output

### Extension Points

- `internal/agent/agent.go`: Core loop, add new phases
- `internal/tools/`: Add new tools by implementing interface
- `internal/llm/`: Add new providers
- `internal/config/`: Add new config sections

## Implementation Phases

### Phase 1: Foundation
- [ ] Project skeleton and module setup
- [ ] Config loading (global + project)
- [ ] Basic CLI with `-h`, `-l`, `-d`
- [ ] Filesystem sandbox
- [ ] Ignore pattern matching

### Phase 2: LLM Layer
- [ ] Provider interface (with LoadModel/UnloadModel)
- [ ] OpenAI provider (raw HTTP)
- [ ] Anthropic provider (raw HTTP)
- [ ] LM Studio provider (with model management)
- [ ] Ollama provider (with model management)
- [ ] Token counting utilities

### Phase 3: Tool System
- [ ] Tool interface and registry
- [ ] Basic tools: read_file, write_file, glob, grep
- [ ] Sensitive tools: bash, web_search, web_fetch
- [ ] Whitelist enforcement

### Phase 4: Agent Core
- [ ] Worker implementation
- [ ] Reviewer implementation
- [ ] Iteration loop
- [ ] Model switching logic (worker↔reviewer)
- [ ] SUMMARY.md / FEEDBACK.md generation
- [ ] Token limit bail logic

### Phase 5: Polish
- [ ] Error handling refinements
- [ ] Debug output (`-d` flag)
- [ ] Iteration timing display
- [ ] Testing and bug fixes

## Dependencies

Only standard library packages:
- `net/http` - HTTP client
- `encoding/json` - JSON handling
- `regexp` - Pattern matching
- `path/filepath` - Path manipulation
- `os`, `io`, `strings`, etc.

No external dependencies required.

## Success Criteria

1. Can successfully run the ralph loop on a sample PRD
2. Respects all safety constraints (sandbox, whitelist)
3. Handles token limits gracefully via bail-to-summary
4. Config hot-reloading works (change config mid-run, picked up next iteration)
5. Human-readable SUMMARY.md and FEEDBACK.md
6. Minimal, clean Go code with clear extension points
