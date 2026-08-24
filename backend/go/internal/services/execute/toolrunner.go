package execute

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// renameFunc is a package-level variable for os.Rename to enable testing of retry logic
var renameFunc = os.Rename

// ToolRunner runs external tools like qaac, lame
type ToolRunner struct {
	toolsConfig ToolsConfig
	rootPath    string
}

// NewToolRunner creates a new tool runner
func NewToolRunner(toolsConfig ToolsConfig) *ToolRunner {
	return &ToolRunner{toolsConfig: toolsConfig}
}

// NewToolRunnerWithRoot creates a new tool runner with root path for soft delete
func NewToolRunnerWithRoot(toolsConfig ToolsConfig, rootPath string) *ToolRunner {
	return &ToolRunner{toolsConfig: toolsConfig, rootPath: rootPath}
}

// ToolError represents a tool execution error
type ToolError struct {
	Code    errdomain.DomainErrorCode
	Message string
	Err     error
}

func (e *ToolError) Error() string {
	return e.Message
}

// Unwrap returns the underlying error
func (e *ToolError) Unwrap() error {
	return e.Err
}

// Convert converts a file using qaac or lame
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

	encoder := r.toolsConfig.Encoder

	switch encoder {
	case "qaac":
		return r.convertWithQAAC(absSrc, absDst)
	case "lame":
		return r.convertWithLAME(absSrc, absDst)
	default:
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "invalid encoder: must be 'qaac' or 'lame'",
			Err:     errors.New("invalid encoder"),
		}
	}
}

// convertWithQAAC runs qaac to convert a file
func (r *ToolRunner) convertWithQAAC(src, dst string) error {
	qaacPath := r.toolsConfig.QAACPath
	if qaacPath == "" {
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "qaac_path not configured",
			Err:     errors.New("qaac_path not set"),
		}
	}

	// Check if qaac exists
	if _, err := exec.LookPath(qaacPath); err != nil {
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "qaac not found",
			Err:     err,
		}
	}

	// Build qaac command: [qaac_path, "--ignorelength", "--no-optimize", "-s", "-v", "256", "-o", dst, src]
	cmd := exec.Command(
		qaacPath,
		"--ignorelength",
		"--no-optimize",
		"-s",
		"-v", "256",
		"-o", dst,
		src,
	)

	// Execute in the same directory as source to handle relative paths
	cmd.Dir = filepath.Dir(src)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check for specific errors
		outputStr := string(output)
		if contains(outputStr, "Permission denied") || contains(outputStr, "cannot open") {
			return &ToolError{
				Code:    errdomain.FILE_LOCKED,
				Message: "file is locked",
				Err:     err,
			}
		}
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "conversion failed: " + outputStr,
			Err:     err,
		}
	}

	return nil
}

// convertWithLAME runs lame to convert a file
func (r *ToolRunner) convertWithLAME(src, dst string) error {
	lamePath := r.toolsConfig.LAMEPath
	if lamePath == "" {
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "lame_path not configured",
			Err:     errors.New("lame_path not set"),
		}
	}

	// Check if lame exists
	if _, err := exec.LookPath(lamePath); err != nil {
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "lame not found",
			Err:     err,
		}
	}

	// Build lame command: [lame_path, "-b", "320", src, dst]
	cmd := exec.Command(
		lamePath,
		"-b", "320",
		src,
		dst,
	)

	// Execute in the same directory as source to handle relative paths
	cmd.Dir = filepath.Dir(src)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check for specific errors
		outputStr := string(output)
		if contains(outputStr, "Permission denied") || contains(outputStr, "cannot open") {
			return &ToolError{
				Code:    errdomain.FILE_LOCKED,
				Message: "file is locked",
				Err:     err,
			}
		}
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "conversion failed: " + outputStr,
			Err:     err,
		}
	}

	return nil
}

// Delete removes a file (with optional soft delete)
// soft=true: move to Delete/<relative_path> relative to rootPath
// soft=false: hard delete
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

// isPermissionDenied checks if the error is a permission denied error
func isPermissionDenied(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return errors.Is(pathErr.Err, os.ErrPermission)
	}
	return false
}

// isLockedOrBusy checks if the error indicates a locked or busy file
func isLockedOrBusy(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
