package execute

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func moveToPersistedTarget(src, target string) error {
	if src == "" || target == "" {
		return fmt.Errorf("source or target path is empty")
	}
	if !filepath.IsAbs(src) {
		return fmt.Errorf("source path must be absolute: %s", src)
	}
	if !filepath.IsAbs(target) {
		return fmt.Errorf("target path must be absolute: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		// Check for permission errors during directory creation
		if isPermissionError(err) {
			return fmt.Errorf("PERMISSION_DENIED: create target dir: %w", err)
		}
		return fmt.Errorf("create target dir: %w", err)
	}
	if err := os.Rename(src, target); err != nil {
		// Check for permission errors during rename
		if isPermissionError(err) {
			return fmt.Errorf("PERMISSION_DENIED: move to persisted target failed: %w", err)
		}
		return fmt.Errorf("move to persisted target failed: %w", err)
	}
	return nil
}

// isPermissionError checks if the error is a permission denied error.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	// Check for os.ErrPermission (works on Unix)
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	// Check for Windows permission error strings
	errStr := err.Error()
	return strings.Contains(errStr, "Access is denied") ||
		strings.Contains(errStr, "ACCESS_DENIED") ||
		strings.Contains(errStr, "permission denied")
}
