//go:build linux

package fileops

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames src to dst and fails when dst exists. Linux gets the
// real primitive: renameat2(RENAME_NOREPLACE) makes the existence check and the
// move one atomic step, so a destination that appears between them is refused
// by the kernel instead of being overwritten.
//
// A filesystem that does not implement the flag (EINVAL, ENOSYS, EOPNOTSUPP)
// falls back to a check immediately before os.Rename — the same guarantee the
// other platforms get.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EOPNOTSUPP):
		return renameChecked(src, dst)
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}

// renameChecked is the portable fallback: refuse an existing destination right
// before the rename.
func renameChecked(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: fs.ErrExist}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return os.Rename(src, dst)
}
