// Package fs provides filesystem sandbox and ignore pattern matching.
package fs

import (
	"path/filepath"
	"strings"
)

// Gitignore provides pattern matching for ignore rules.
// It supports basic glob patterns: *, ?, **
type Gitignore struct {
	patterns []string
}

// NewGitignore creates a new Gitignore matcher with the given patterns.
func NewGitignore(patterns []string) *Gitignore {
	// Clean patterns - remove empty ones
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p != "" && !strings.HasPrefix(p, "#") {
			cleaned = append(cleaned, p)
		}
	}
	return &Gitignore{patterns: cleaned}
}

// Match returns true if the given path matches any of the ignore patterns.
func (g *Gitignore) Match(path string) bool {
	// Clean the path
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")

	for _, pattern := range g.patterns {
		if matchPattern(pattern, path) {
			return true
		}
	}
	return false
}

// matchPattern matches a single pattern against a path.
// Supports:
//   - *: matches any sequence of non-separator characters
//   - **: matches any sequence of characters including separators
//   - ?: matches any single non-separator character
func matchPattern(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	pattern = strings.TrimSpace(pattern)

	// Handle directory-only patterns (ending with /)
	isDirPattern := strings.HasSuffix(pattern, "/")
	if isDirPattern {
		pattern = strings.TrimSuffix(pattern, "/")
	}

	// Handle patterns with **
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, path, isDirPattern)
	}

	// Handle patterns with /
	if strings.Contains(pattern, "/") {
		return matchWithSlash(pattern, path, isDirPattern)
	}

	// Simple glob pattern - match against any component
	return matchSimpleGlob(pattern, path, isDirPattern)
}

// matchDoubleStar handles patterns containing **
func matchDoubleStar(pattern, path string, isDirPattern bool) bool {
	// Split pattern by **
	parts := strings.Split(pattern, "**")

	if len(parts) == 1 {
		// No ** (shouldn't happen)
		return matchSimpleGlob(pattern, path, isDirPattern)
	}

	// ** at the start: **/foo matches any path ending with /foo
	if pattern == "**" {
		return true
	}

	if strings.HasPrefix(pattern, "**") {
		// Pattern starts with **
	suffix := parts[1]
		if strings.HasPrefix(suffix, "/") {
			suffix = suffix[1:]
		}
		return hasSuffixMatch(suffix, path)
	}

	if strings.HasSuffix(pattern, "**") {
		// Pattern ends with **
		prefix := parts[0]
		if strings.HasSuffix(prefix, "/") {
			prefix = prefix[:len(prefix)-1]
		}
		return hasPrefixMatch(prefix, path)
	}

	// ** in the middle
	prefix := parts[0]
	suffix := parts[1]
	if strings.HasSuffix(prefix, "/") {
		prefix = prefix[:len(prefix)-1]
	}
	if strings.HasPrefix(suffix, "/") {
		suffix = suffix[1:]
	}

	return hasPrefixMatch(prefix, path) && hasSuffixMatch(suffix, path)
}

// hasPrefixMatch checks if path starts with the given prefix pattern.
func hasPrefixMatch(prefix, path string) bool {
	pathParts := strings.Split(path, "/")
	prefixParts := strings.Split(prefix, "/")

	if len(prefixParts) > len(pathParts) {
		return false
	}

	for i, p := range prefixParts {
		if !matchGlob(p, pathParts[i]) {
			return false
		}
	}
	return true
}

// hasSuffixMatch checks if path ends with the given suffix pattern.
func hasSuffixMatch(suffix, path string) bool {
	pathParts := strings.Split(path, "/")
	suffixParts := strings.Split(suffix, "/")

	if len(suffixParts) > len(pathParts) {
		return false
	}

	startIdx := len(pathParts) - len(suffixParts)
	for i, s := range suffixParts {
		if !matchGlob(s, pathParts[startIdx+i]) {
			return false
		}
	}
	return true
}

// matchWithSlash handles patterns containing / but no **
func matchWithSlash(pattern, path string, isDirPattern bool) bool {
	// Pattern like "foo/bar" - must match from start
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(patternParts) > len(pathParts) {
		return false
	}

	for i, p := range patternParts {
		if !matchGlob(p, pathParts[i]) {
			return false
		}
	}

	return true
}

// matchSimpleGlob matches a pattern against any component of the path.
func matchSimpleGlob(pattern, path string, isDirPattern bool) bool {
	// Pattern without / matches against any path component
	pathParts := strings.Split(path, "/")

	for _, part := range pathParts {
		if matchGlob(pattern, part) {
			return true
		}
	}
	return false
}

// matchGlob matches a single glob pattern against a string.
// Supports * and ? only (not ** which is handled separately).
func matchGlob(pattern, str string) bool {
	// Simple case: exact match
	if pattern == str {
		return true
	}

	// Handle ? wildcard (single character)
	// Handle * wildcard (zero or more characters)
	
	// Use dynamic programming approach for glob matching
	pLen := len(pattern)
	sLen := len(str)

	// dp[i][j] = true if pattern[0:i] matches str[0:j]
	// Use 1D array for space efficiency
	dp := make([]bool, sLen+1)
	dp[0] = true

	// Handle pattern starting with *
	for i := 0; i < pLen && pattern[i] == '*'; i++ {
		for j := 0; j <= sLen; j++ {
			dp[j] = true
		}
	}

	for i := 0; i < pLen; i++ {
		newDp := make([]bool, sLen+1)
		p := pattern[i]

		for j := 0; j < sLen; j++ {
			if !dp[j] {
				continue
			}

			switch p {
			case '*':
				// * matches zero or more characters
				for k := j; k <= sLen; k++ {
					newDp[k] = true
				}
			case '?':
				// ? matches any single character
				newDp[j+1] = true
			default:
				// Exact character match
				if p == str[j] {
					newDp[j+1] = true
				}
			}
		}

		// Handle * matching zero characters at end
		if p == '*' && dp[sLen] {
			newDp[sLen] = true
		}

		dp = newDp
	}

	return dp[sLen]
}
