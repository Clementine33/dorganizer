package conversion

import (
	"path/filepath"
)

// normalizeScopePath cleans a scope path to canonical POSIX form.
func normalizeScopePath(path string) string {
	native := filepath.FromSlash(path)
	cleaned := filepath.Clean(native)
	return filepath.ToSlash(cleaned)
}
