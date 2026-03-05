// Package tools provides the tool system for agent operations.
package tools

import (
	"context"
	"fmt"
	"sync"

	"github.com/tuffrabit/boberto/internal/config"
)

// Registry manages the collection of available tools.
type Registry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// DefaultRegistry is the global default registry.
// Tools register themselves to this registry via init() functions.
var DefaultRegistry = NewRegistry()

// Register adds a tool to the registry.
// Returns an error if a tool with the same name is already registered.
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}

	r.tools[name] = tool
	return nil
}

// Register adds a tool to the default registry.
func Register(tool Tool) error {
	return DefaultRegistry.Register(tool)
}

// Get retrieves a tool by name from the registry.
// Returns nil if the tool is not found.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.tools[name]
}

// Get retrieves a tool by name from the default registry.
func Get(name string) Tool {
	return DefaultRegistry.Get(name)
}

// GetAll returns all registered tools.
func (r *Registry) GetAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetAll returns all tools from the default registry.
func GetAll() []Tool {
	return DefaultRegistry.GetAll()
}

// GetAvailable returns all tools that are available given the whitelist.
// Non-sensitive tools are always available.
// Sensitive tools are available but still require whitelist checks at execution time.
func (r *Registry) GetAvailable(whitelist config.Whitelist) []Tool {
	// For now, return all tools - whitelist is checked at execution time
	// This allows the LLM to see all available tools and get meaningful
	// error messages if they try to use a sensitive tool that's not allowed
	return r.GetAll()
}

// GetAvailable returns all available tools from the default registry.
func GetAvailable(whitelist config.Whitelist) []Tool {
	return DefaultRegistry.GetAvailable(whitelist)
}

// Execute runs a tool by name with the given arguments.
// The whitelist is passed to sensitive tools for permission checking.
func (r *Registry) Execute(ctx context.Context, name string, args map[string]any, whitelist config.Whitelist) (Result, error) {
	tool := r.Get(name)
	if tool == nil {
		return Failure(fmt.Sprintf("tool not found: %s", name)), fmt.Errorf("tool not found: %s", name)
	}

	return tool.Execute(ctx, args, whitelist)
}

// Execute runs a tool from the default registry.
func Execute(ctx context.Context, name string, args map[string]any, whitelist config.Whitelist) (Result, error) {
	return DefaultRegistry.Execute(ctx, name, args, whitelist)
}

// ToolDefinitions returns all tool definitions in the format expected by LLM providers.
func (r *Registry) ToolDefinitions() []map[string]any {
	tools := r.GetAll()
	definitions := make([]map[string]any, len(tools))
	for i, tool := range tools {
		definitions[i] = ToolDefinition(tool)
	}
	return definitions
}

// ToolDefinitions returns all tool definitions from the default registry.
func ToolDefinitions() []map[string]any {
	return DefaultRegistry.ToolDefinitions()
}

// SensitiveTools returns a list of sensitive tool names.
// These tools require whitelist validation.
func (r *Registry) SensitiveTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var names []string
	for name, tool := range r.tools {
		if tool.IsSensitive() {
			names = append(names, name)
		}
	}
	return names
}

// SensitiveTools returns sensitive tool names from the default registry.
func SensitiveTools() []string {
	return DefaultRegistry.SensitiveTools()
}
