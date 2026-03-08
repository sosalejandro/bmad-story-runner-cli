package cmd

import (
	"path/filepath"
	"strings"
)

func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}

// absPath resolves a path argument to absolute. Returns the path unchanged if
// resolution fails (let downstream report the real error).
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// looksLikePath returns true if the string looks like a file/directory path.
func looksLikePath(s string) bool {
	return strings.Contains(s, "/") || strings.Contains(s, ".json") ||
		strings.Contains(s, ".md") || strings.HasPrefix(s, ".")
}
