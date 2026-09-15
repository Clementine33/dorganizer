package plan

import (
	"path/filepath"
	"strings"
)

// normalizeScopePath cleans a scope path to canonical POSIX form.
func normalizeScopePath(path string) string {
	native := filepath.FromSlash(path)
	cleaned := filepath.Clean(native)
	return filepath.ToSlash(cleaned)
}

// escapeLikePattern escapes SQL LIKE wildcards so a user-supplied path can
// never widen a LIKE scope.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
