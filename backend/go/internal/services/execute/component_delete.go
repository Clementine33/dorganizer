package execute

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// recoveryDirName is the in-root recovery folder of the soft-delete
// convention, preserved from the legacy runner.
const recoveryDirName = "Delete"

// maxRecoveryNameAttempts bounds unique-name generation below Delete/.
const maxRecoveryNameAttempts = 100

// removeObsolete removes one obsolete file per the selected mode. Soft removal
// reports the recovery location in persisted form; hard removal reports none.
func removeObsolete(tk *componentToolkit, absRoot, source string, soft bool) (string, error) {
	if !soft {
		if err := tk.remove(source); err != nil {
			return "", fmt.Errorf("hard delete %s: %w", posixForm(source), err)
		}
		return "", nil
	}
	dest, err := softRemove(tk, absRoot, source)
	if err != nil {
		return "", err
	}
	return posixForm(dest), nil
}

// softRemove moves a file into <root>/Delete/<relative path>, preserving the
// legacy recovery convention. An existing recovery file is never overwritten:
// a collision gets the first free "<stem>.N<ext>" name. The destination is
// returned so callers can report where the media can be recovered.
func softRemove(tk *componentToolkit, absRoot, source string) (string, error) {
	rel, err := filepath.Rel(absRoot, source)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("compute recovery path for %s: not inside the member root", posixForm(source))
	}
	relDir := filepath.Dir(rel)
	if pathnorm.IsWindowsUNCPath(absRoot) {
		relDir = pathnorm.TruncatePathComponentsToBytes(relDir, 214)
	}
	deleteDir := filepath.Join(absRoot, recoveryDirName, relDir)
	if mkdirErr := os.MkdirAll(deleteDir, 0o755); mkdirErr != nil {
		return "", fmt.Errorf("create recovery directory: %w", mkdirErr)
	}
	dest, err := uniqueRecoveryPath(deleteDir, filepath.Base(source))
	if err != nil {
		return "", err
	}
	if err := renameWithLockRetry(tk, source, dest); err != nil {
		return "", fmt.Errorf("move to recovery: %w", err)
	}
	return dest, nil
}

// uniqueRecoveryPath returns the first free name for base inside dir, so an
// earlier recovery file is never overwritten.
func uniqueRecoveryPath(dir, base string) (string, error) {
	for attempt := 0; attempt <= maxRecoveryNameAttempts; attempt++ {
		candidate := filepath.Join(dir, recoveryName(base, attempt))
		_, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect recovery name %s: %w", posixForm(candidate), err)
		}
	}
	return "", fmt.Errorf("no free recovery name for %s after %d attempts", base, maxRecoveryNameAttempts)
}

// recoveryName is base itself for attempt 0 and "<stem>.<N><ext>" afterwards.
func recoveryName(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	ext := filepath.Ext(base)
	return fmt.Sprintf("%s.%d%s", strings.TrimSuffix(base, ext), attempt, ext)
}

// renameWithLockRetry mirrors the legacy soft-delete retry policy: locked or
// busy files get exponential backoff, permission errors fail immediately.
func renameWithLockRetry(tk *componentToolkit, oldpath, newpath string) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := tk.rename(oldpath, newpath)
		if err == nil {
			return nil
		}
		lastErr = err
		if isPermissionDenied(err) {
			return err
		}
		if isLockedOrBusy(err) && attempt < maxAttempts {
			time.Sleep(time.Duration(attempt*100) * time.Millisecond)
			continue
		}
		break
	}
	return lastErr
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
		// Windows locked/busy indicators.
		if strings.Contains(errStr, "locked") ||
			strings.Contains(errStr, "being used") ||
			strings.Contains(errStr, "accessed by another process") ||
			strings.Contains(errStr, "file is locked") {
			return true
		}
	}
	// Also check for generic locked error patterns.
	errStr := err.Error()
	return strings.Contains(errStr, "locked") ||
		strings.Contains(errStr, "being used") ||
		strings.Contains(errStr, "accessed by another process") ||
		strings.Contains(errStr, "file is locked")
}
