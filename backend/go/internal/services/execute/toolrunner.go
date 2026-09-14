package execute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// renameFunc is a package-level variable for os.Rename to enable testing of retry logic.
var renameFunc = os.Rename

// ToolRunner runs ffmpeg conversion and filesystem deletion.
type ToolRunner struct {
	toolsConfig ToolsConfig
	rootPath    string
}

// NewToolRunner creates a new tool runner.
func NewToolRunner(toolsConfig ToolsConfig) *ToolRunner {
	return &ToolRunner{toolsConfig: toolsConfig}
}

// NewToolRunnerWithRoot creates a new tool runner with root path for soft delete.
func NewToolRunnerWithRoot(toolsConfig ToolsConfig, rootPath string) *ToolRunner {
	return &ToolRunner{toolsConfig: toolsConfig, rootPath: rootPath}
}

// ToolError represents a tool execution error.
type ToolError struct {
	Code    errdomain.DomainErrorCode
	Message string
	Err     error
}

func (e *ToolError) Error() string {
	return e.Message
}

// Unwrap returns the underlying error.
func (e *ToolError) Unwrap() error {
	return e.Err
}

// Convert uses the destination codec and the legacy default bitrate.
func (r *ToolRunner) Convert(src, dst string) error {
	absSrc, err := validateAbsolutePath(src)
	if err != nil {
		return &ToolError{
			Code:    errdomain.FILE_LOCKED,
			Message: "source path must be absolute",
			Err:     err,
		}
	}

	absDst, err := validateAbsolutePath(dst)
	if err != nil {
		return &ToolError{
			Code:    errdomain.FILE_LOCKED,
			Message: "destination path must be absolute",
			Err:     err,
		}
	}

	spec, err := legacyTargetSpec(absDst)
	if err != nil {
		return &ToolError{Code: errdomain.TOOL_NOT_FOUND, Message: err.Error(), Err: err}
	}
	encoder := newFFmpeg(r.toolsConfig)
	if err := encoder.Check(); err != nil {
		return &ToolError{Code: errdomain.TOOL_NOT_FOUND, Message: err.Error(), Err: err}
	}
	if err := encoder.Encode(context.Background(), absSrc, absDst, spec); err != nil {
		return &ToolError{Code: errdomain.FILE_LOCKED, Message: err.Error(), Err: err}
	}
	return nil
}

// Delete removes a file (with optional soft delete)
// soft=true: move to Delete/<relative_path> relative to rootPath
// soft=false: hard delete.
func (r *ToolRunner) Delete(path string, soft bool) error {
	absPath, err := validateAbsolutePath(path)
	if err != nil {
		return &ToolError{
			Code:    errdomain.FILE_LOCKED,
			Message: "delete path must be absolute",
			Err:     err,
		}
	}

	if soft && r.rootPath != "" {
		return r.softDelete(absPath)
	}
	return os.Remove(absPath)
}

// softDelete moves the file to <root>/Delete/<relPath>
// Retry policy:
//   - Locked/Busy: exponential backoff, max 3 attempts
//   - Permission denied: immediate error, no retry
//   - Other errors: immediate error
func (r *ToolRunner) softDelete(path string) error {
	absRoot, err := validateAbsolutePath(r.rootPath)
	if err != nil {
		return &ToolError{
			Code:    errdomain.FILE_LOCKED,
			Message: "soft delete root path must be absolute",
			Err:     err,
		}
	}

	relPath, err := filepath.Rel(absRoot, path)
	if err != nil {
		return &ToolError{
			Code:    errdomain.FILE_LOCKED,
			Message: "failed to compute relative path for soft delete",
			Err:     err,
		}
	}

	relDir := filepath.Dir(relPath)
	if pathnorm.IsWindowsUNCPath(absRoot) {
		relDir = pathnorm.TruncatePathComponentsToBytes(relDir, 214)
	}

	deleteDir := filepath.Join(absRoot, "Delete", relDir)
	if err := os.MkdirAll(deleteDir, 0755); err != nil {
		return &ToolError{
			Code:    errdomain.FILE_LOCKED,
			Message: "failed to create delete directory",
			Err:     err,
		}
	}

	destPath := filepath.Join(deleteDir, filepath.Base(path))

	// Retry with exponential backoff for locked/busy errors
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := renameFunc(path, destPath)
		if err == nil {
			return nil // success
		}
		lastErr = err

		// Check if error is permission denied - no retry
		if isPermissionDenied(err) {
			return &ToolError{
				Code:    errdomain.FILE_LOCKED,
				Message: "PERMISSION_DENIED: soft delete failed due to permission denied",
				Err:     err,
			}
		}

		// Check if error is locked/busy - retry with backoff
		if isLockedOrBusy(err) && attempt < maxAttempts {
			// Exponential backoff: 100ms, 200ms
			backoff := time.Duration(attempt*100) * time.Millisecond
			time.Sleep(backoff)
			continue
		}

		// Other errors or max retries reached - return immediately
		break
	}

	return &ToolError{
		Code:    errdomain.FILE_LOCKED,
		Message: "soft delete failed after retries",
		Err:     lastErr,
	}
}

// isPermissionDenied checks if the error is a permission denied error.
func isPermissionDenied(err error) bool {
	if pathErr, ok := errors.AsType[*os.PathError](err); ok {
		return errors.Is(pathErr.Err, os.ErrPermission)
	}
	return false
}

// isLockedOrBusy checks if the error indicates a locked or busy file.
func isLockedOrBusy(err error) bool {
	if pathErr, ok := errors.AsType[*os.PathError](err); ok {
		errStr := pathErr.Err.Error()
		// Windows locked/busy indicators
		if strings.Contains(errStr, "locked") ||
			strings.Contains(errStr, "being used") ||
			strings.Contains(errStr, "accessed by another process") ||
			strings.Contains(errStr, "file is locked") {
			return true
		}
	}
	// Also check for generic locked error patterns
	errStr := err.Error()
	return strings.Contains(errStr, "locked") ||
		strings.Contains(errStr, "being used") ||
		strings.Contains(errStr, "accessed by another process") ||
		strings.Contains(errStr, "file is locked")
}

func validateAbsolutePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("path is not absolute")
	}
	return filepath.Abs(path)
}
