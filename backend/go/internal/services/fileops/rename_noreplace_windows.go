//go:build windows

package fileops

import (
	"os"
	"syscall"
)

// renameNoReplace renames src to dst and fails when dst exists. Windows gets
// the real primitive: MoveFileW refuses an existing destination, which is
// exactly the no-overwrite guarantee this operation needs.
func renameNoReplace(src, dst string) error {
	srcPtr, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	dstPtr, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	if err := syscall.MoveFile(srcPtr, dstPtr); err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return nil
}
