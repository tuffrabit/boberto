// Package fs provides filesystem sandbox and ignore pattern matching.
package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sandbox enforces filesystem boundaries and ignore patterns.
type Sandbox struct {
	Root   string
	Ignore *Gitignore
}

// NewSandbox creates a new sandbox for the given project root.
func NewSandbox(root string, ignore *Gitignore) *Sandbox {
	return &Sandbox{
		Root:   filepath.Clean(root),
		Ignore: ignore,
	}
}

// Validate validates a path and returns the absolute path within the sandbox.
// Returns an error if the path:
//   - Contains path traversal (..)
//   - Is an absolute path outside the root
//   - Is a symlink (symlinks are rejected in Phase 1)
//   - Resolves to a location outside the root
//   - Matches an ignore pattern
func (s *Sandbox) Validate(path string) (string, error) {
	// Clean the input path
	path = filepath.Clean(path)

	// Reject paths containing .. traversal
	if strings.Contains(path, "..") {
		return "", fmt.Errorf("path contains traversal: %s", path)
	}

	// Determine the absolute path
	var absPath string
	if filepath.IsAbs(path) {
		absPath = path
	} else {
		absPath = filepath.Join(s.Root, path)
	}

	absPath = filepath.Clean(absPath)

	// Ensure the path is within the root
	// Use strings.HasPrefix with trailing separator to avoid partial matches
	rootWithSep := s.Root + string(filepath.Separator)
	if !strings.HasPrefix(absPath, rootWithSep) && absPath != s.Root {
		return "", fmt.Errorf("path outside sandbox: %s", path)
	}

	// Check for symlinks (rejected in Phase 1)
	fi, err := os.Lstat(absPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to stat path: %w", err)
	}
	if err == nil {
		// File exists, check if it's a symlink
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinks not allowed: %s", path)
		}
	}

	// For symlinks along the path components, we need to check each component
	// Walk the path and check each component
	if err := s.checkPathComponents(absPath); err != nil {
		return "", err
	}

	// Check ignore patterns
	if s.Ignore != nil {
		relPath, err := filepath.Rel(s.Root, absPath)
		if err != nil {
			return "", fmt.Errorf("failed to get relative path: %w", err)
		}
		if s.Ignore.Match(relPath) {
			return "", fmt.Errorf("path matches ignore pattern: %s", relPath)
		}
	}

	return absPath, nil
}

// checkPathComponents checks each component of the path for symlinks.
func (s *Sandbox) checkPathComponents(absPath string) error {
	// Start from root and walk each component
	current := s.Root

	// Get the path relative to root
	relPath, err := filepath.Rel(s.Root, absPath)
	if err != nil {
		return err
	}

	// Handle case where path is the root itself
	if relPath == "." {
		fi, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("root is a symlink: %s", current)
		}
		return nil
	}

	parts := strings.Split(relPath, string(filepath.Separator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)

		fi, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				// Path doesn't exist yet, that's ok
				continue
			}
			return err
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks not allowed: %s", current)
		}
	}

	return nil
}

// ReadFile reads a file within the sandbox.
func (s *Sandbox) ReadFile(path string) ([]byte, error) {
	validPath, err := s.Validate(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(validPath)
}

// WriteFile writes a file within the sandbox.
func (s *Sandbox) WriteFile(path string, data []byte, perm os.FileMode) error {
	validPath, err := s.Validate(path)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(validPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	return os.WriteFile(validPath, data, perm)
}

// Stat returns file info for a path within the sandbox.
func (s *Sandbox) Stat(path string) (os.FileInfo, error) {
	validPath, err := s.Validate(path)
	if err != nil {
		return nil, err
	}
	return os.Stat(validPath)
}
