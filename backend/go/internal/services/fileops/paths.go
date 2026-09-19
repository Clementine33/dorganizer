package fileops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// Path error codes reported per item. They are stable machine codes: the UI
// maps them to words, the tests assert on them.
const (
	CodePathInvalid     = "PATH_INVALID"
	CodeOutsideMember   = "OUTSIDE_MEMBER"
	CodeMemberRoot      = "MEMBER_ROOT"
	CodeSymlink         = "SYMLINK"
	CodeSourceMissing   = "SOURCE_MISSING"
	CodeTargetExists    = "TARGET_EXISTS"
	CodeMoveIntoSelf    = "MOVE_INTO_SELF"
	CodeInvalidName     = "INVALID_NAME"
	CodeInvalidTarget   = "INVALID_TARGET_DIR"
	CodePermission      = "PERMISSION_DENIED"
	CodeLocked          = "FILE_LOCKED"
	CodeIOError         = "IO_ERROR"
	CodeMemberMissing   = "MEMBER_MISSING"
	CodeOperationDenied = "OPERATION_DENIED"
)

// member is the resolved member directory one request works inside.
type member struct {
	root       string // absolute library root
	rel        string // library-relative member path
	abs        string // absolute member directory
	nativeAbs  string // the same path in the host's separator form
	nativeRoot string
}

// openMember validates a library-relative member path and resolves it. The
// member is the scope of every file operation: it must be a direct child
// directory of the library root (the recovery directory is never one), and it
// must exist on disk as a real directory — a symlink is not a member, so
// nothing below it would be reachable through a link.
func openMember(rootPath, memberRel string) (*member, error) {
	if strings.TrimSpace(rootPath) == "" {
		return nil, &PathError{CodePathInvalid, "the library root is unknown"}
	}
	rel, ok := pathnorm.RelPath(memberRel)
	if !ok || strings.Contains(rel, "/") {
		return nil, &PathError{CodePathInvalid, "a member path is one library-relative directory name"}
	}
	if isRecoveryName(rootPath, rel) {
		return nil, &PathError{CodeMemberRoot, "the recovery directory is not a member"}
	}
	m := &member{
		root:       pathnorm.NormalizeToPOSIX(rootPath),
		rel:        rel,
		abs:        pathnorm.JoinRel(rootPath, rel),
		nativeAbs:  filepath.FromSlash(pathnorm.JoinRel(rootPath, rel)),
		nativeRoot: filepath.FromSlash(pathnorm.NormalizeToPOSIX(rootPath)),
	}
	info, err := os.Lstat(m.nativeAbs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &PathError{CodeMemberMissing, "the member directory does not exist"}
		}
		return nil, &PathError{errCode(err), err.Error()}
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, &PathError{CodeSymlink, "the member directory is a symbolic link"}
	}
	if !info.IsDir() {
		return nil, &PathError{CodeMemberRoot, "the member path is not a directory"}
	}
	return m, nil
}

// resolve validates a member-relative path and returns its native form. Every
// component from the member down to the item must exist and must not be a
// symbolic link: the first version offers no operation on a link or through
// one, so a link can never be used to reach outside the member (spec F3, T2).
func (m *member) resolve(rel string) (string, error) {
	clean, ok := pathnorm.RelPath(rel)
	if !ok {
		return "", &PathError{CodePathInvalid, "the path is not a member-relative path"}
	}
	native := m.nativeAbs
	for segment := range strings.SplitSeq(clean, "/") {
		native = filepath.Join(native, filepath.FromSlash(segment))
		info, err := os.Lstat(native)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", &PathError{CodeSourceMissing, "the path does not exist"}
			}
			return "", &PathError{errCode(err), err.Error()}
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", &PathError{CodeSymlink, "the path goes through a symbolic link"}
		}
	}
	// Containment is decided on resolved forms as well as on the shape of the
	// relative path, so a path that is lexically inside but leaves through a
	// link is refused rather than trusted.
	if !insideMember(m.nativeAbs, native) {
		return "", &PathError{CodeOutsideMember, "the path is outside the member"}
	}
	return native, nil
}

// resolveTargetDir validates a member-relative directory used as a move
// destination: it must already exist, be a real directory, and never be a
// symlink.
func (m *member) resolveTargetDir(rel string) (string, error) {
	native, err := m.resolve(rel)
	if err != nil {
		var itemErr *PathError
		if errors.As(err, &itemErr) && itemErr.Code == CodeSourceMissing {
			return "", &PathError{CodeInvalidTarget, "the destination directory does not exist"}
		}
		return "", err
	}
	info, statErr := os.Lstat(native)
	if statErr != nil {
		return "", &PathError{errCode(statErr), statErr.Error()}
	}
	if !info.IsDir() {
		return "", &PathError{CodeInvalidTarget, "the destination is not a directory"}
	}
	return native, nil
}

// targetName validates the new name of a rename. A name is one path component:
// a separator, a traversal segment or an empty string is refused, so a rename
// can never move an item to another directory (spec F1).
func targetName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed != name {
		return "", &PathError{CodeInvalidName, "the new name is empty or padded with spaces"}
	}
	if _, ok := pathnorm.RelPath(name); !ok || strings.Contains(name, "/") {
		return "", &PathError{CodeInvalidName, "the new name is a plain file or folder name"}
	}
	if name == "." || name == ".." {
		return "", &PathError{CodeInvalidName, "the new name is not a directory reference"}
	}
	return name, nil
}

// insideMember reports whether candidate is base itself or one of its
// descendants, comparing cleaned native paths at a separator boundary.
func insideMember(base, candidate string) bool {
	cleanBase := filepath.Clean(base)
	cleanCandidate := filepath.Clean(candidate)
	if cleanCandidate == cleanBase {
		return true
	}
	return strings.HasPrefix(cleanCandidate, cleanBase+string(filepath.Separator))
}

// errCode maps a filesystem error to the stable code the UI explains.
func errCode(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return CodePermission
	case errors.Is(err, fs.ErrNotExist):
		return CodeSourceMissing
	case errors.Is(err, fs.ErrExist):
		return CodeTargetExists
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"locked", "being used", "accessed by another process"} {
		if strings.Contains(message, marker) {
			return CodeLocked
		}
	}
	return CodeIOError
}

// PathError is one refused path or operation, carrying the stable code the
// caller maps to a response. It is returned both per item (inside a result)
// and for a request that never became a scope at all.
type PathError struct {
	Code    string
	Message string
}

func (e *PathError) Error() string { return e.Code + ": " + e.Message }

// isRecoveryName reports whether a directory name is the library-level
// recovery directory, folding case on a case-insensitive filesystem.
func isRecoveryName(rootPath, name string) bool {
	if name == pathnorm.RecoveryDirName {
		return true
	}
	return pathnorm.IsWindowsCaseInsensitivePath(rootPath) &&
		strings.EqualFold(name, pathnorm.RecoveryDirName)
}
