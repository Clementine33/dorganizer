package execute

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type stageFailureError struct {
	stage     string
	itemIndex int
	err       error
}

func (e *stageFailureError) Error() string {
	if e == nil {
		return ""
	}
	switch e.stage {
	case "stage1":
		return fmt.Sprintf("stage1 copy failed for item %d: %v", e.itemIndex, e.err)
	case "stage2":
		return fmt.Sprintf("stage2 encode failed for item %d: %v", e.itemIndex, e.err)
	case "stage3":
		return fmt.Sprintf("stage3 commit failed for item %d: %v", e.itemIndex, e.err)
	default:
		return fmt.Sprintf("execution failed for item %d: %v", e.itemIndex, e.err)
	}
}

func (e *stageFailureError) Unwrap() error { return e.err }

// getFolderForItem returns the first-level child folder path for the given root and item.
func getFolderForItem(rootPath string, item PlanItem) string {
	if rootPath == "" {
		return ""
	}
	// Attribution precedence aligns with grpc execute handler:
	// source > precondition > target
	candidatePath := item.SourcePath
	if candidatePath == "" {
		candidatePath = item.PreconditionPath
	}
	if candidatePath == "" {
		candidatePath = item.TargetPath
	}
	if candidatePath == "" {
		return ""
	}

	// Normalize and check containment
	absRoot, err := filepath.Abs(filepath.FromSlash(rootPath))
	if err != nil {
		return ""
	}
	absCandidate, err := filepath.Abs(filepath.FromSlash(candidatePath))
	if err != nil {
		return ""
	}
	absRootNorm := filepath.ToSlash(filepath.Clean(absRoot))
	absCandidateNorm := filepath.ToSlash(filepath.Clean(absCandidate))

	// Check containment (case-insensitive on Windows)
	if !strings.HasPrefix(strings.ToLower(absCandidateNorm), strings.ToLower(absRootNorm)+"/") {
		return ""
	}
	if absCandidateNorm == absRootNorm {
		return ""
	}

	// Get first segment (first-level child)
	relPath := absCandidateNorm[len(absRootNorm)+1:]
	relPath = strings.TrimSuffix(relPath, "/")

	// Extract first segment
	parts := strings.Split(relPath, "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}

	// Return first-level child as canonical absolute path
	return absRootNorm + "/" + parts[0]
}

// commitReplace performs safe atomic commit: copy scratchOut to dst.tmp.* then rename.
// Implements Task 4 requirements:
// - Creates unique temp file in dst directory
// - io.Copy + Sync + explicit Close error check
// - os.Rename with Windows retry for ACCESS_DENIED/SHARING_VIOLATION
func (s *ExecuteService) commitReplace(scratchOut, dst string) error {
	// Create unique temp file in same directory as dst
	dstDir := filepath.Dir(dst)
	dstBase := filepath.Base(dst)
	dstTmp := filepath.Join(dstDir, dstBase+".tmp."+uuid.NewString()[:8])

	// Copy scratchOut to dstTmp
	srcFile, err := os.Open(scratchOut)
	if err != nil {
		return fmt.Errorf("open scratchOut: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstTmp)
	if err != nil {
		return fmt.Errorf("create dstTmp: %w", err)
	}

	// io.Copy
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		_ = os.Remove(dstTmp)
		return fmt.Errorf("copy to dstTmp: %w", err)
	}

	// Sync
	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		_ = os.Remove(dstTmp)
		return fmt.Errorf("sync dstTmp: %w", err)
	}

	// Explicit Close error check
	if err := dstFile.Close(); err != nil {
		_ = os.Remove(dstTmp)
		return fmt.Errorf("close dstTmp: %w", err)
	}

	// Rename dstTmp -> dst with Windows retry for ACCESS_DENIED/SHARING_VIOLATION
	if err := s.renameWithRetry(dstTmp, dst); err != nil {
		_ = os.Remove(dstTmp)
		return fmt.Errorf("rename to dst: %w", err)
	}

	return nil
}

// renameWithRetry performs os.Rename with Windows retry for ACCESS_DENIED/SHARING_VIOLATION.
// Uses exponential backoff: 50ms, 100ms, 200ms (3 retries).
func (s *ExecuteService) renameWithRetry(src, dst string) error {
	var lastErr error
	backoffs := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}

	for attempt := 0; attempt <= len(backoffs); attempt++ {
		err := os.Rename(src, dst)
		if err == nil {
			return nil
		}
		lastErr = err

		// Check if this is a Windows ACCESS_DENIED or SHARING_VIOLATION error
		if isWindowsAccessError(err) && attempt < len(backoffs) {
			time.Sleep(backoffs[attempt])
			continue
		}

		// Non-retryable error or final attempt
		break
	}

	return lastErr
}

// isWindowsAccessError checks if the error is Windows ACCESS_DENIED or SHARING_VIOLATION.
func isWindowsAccessError(err error) bool {
	if err == nil {
		return false
	}
	// On Windows, ACCESS_DENIED (0x5) and SHARING_VIOLATION (0x20) errors
	// can occur when files are temporarily locked by antivirus or other processes
	errStr := err.Error()
	return containsStr(errStr, "Access is denied") ||
		containsStr(errStr, "ACCESS_DENIED") ||
		containsStr(errStr, "The process cannot access the file") ||
		containsStr(errStr, "being used by another process") ||
		containsStr(errStr, "SHARING_VIOLATION")
}

// containsStr checks if s contains substr (case-insensitive helper).
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
