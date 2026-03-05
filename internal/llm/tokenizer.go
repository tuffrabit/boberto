// Package llm provides LLM client interfaces and implementations.
package llm

// ApproximateTokenCount estimates the number of tokens in text.
// Uses the approximation: 4 characters ≈ 1 token.
// This is a rough estimate that works reasonably well for English text.
func ApproximateTokenCount(text string) int {
	// Count runes to handle Unicode properly, then divide by 4
	runeCount := 0
	for range text {
		runeCount++
	}
	count := runeCount / 4
	if count == 0 && runeCount > 0 {
		return 1
	}
	return count
}

// approximateTokenCount is the internal implementation used by providers.
// This is an alias to ApproximateTokenCount for internal use.
func approximateTokenCount(text string) int {
	return ApproximateTokenCount(text)
}

// CountMessageTokens estimates the total tokens for a slice of messages.
// Adds overhead for message formatting (role delimiters, etc.).
func CountMessageTokens(messages []Message) int {
	total := 0
	// Base overhead for the conversation format
	total += 3
	
	for _, msg := range messages {
		// Each message has overhead
		total += 4
		// Content tokens
		total += ApproximateTokenCount(msg.Content)
		// Role tokens (approximate)
		total += ApproximateTokenCount(msg.Role)
	}
	
	return total
}

// CountTokensInRequest estimates the total tokens for a request.
// This includes system prompt, messages, and tool definitions.
func CountTokensInRequest(system string, messages []Message, tools []ToolDefinition) int {
	total := 0
	
	// System prompt
	if system != "" {
		total += ApproximateTokenCount(system)
	}
	
	// Messages
	total += CountMessageTokens(messages)
	
	// Tools (rough estimate - tools add significant overhead)
	for _, tool := range tools {
		total += ApproximateTokenCount(tool.Name)
		total += ApproximateTokenCount(tool.Description)
		// Parameters schema is harder to estimate, use a fixed overhead
		total += 50
	}
	
	return total
}
