//go:build !linux && !windows

package fileops

// renameNoReplace renames src to dst and fails when dst exists. This platform
// has no no-replace rename in the standard library, so the destination is
// checked immediately before the move. The window between the check and the
// rename is what the application-wide admission gate closes for every writer
// inside this process (ADR 0002 §1, §2); a program outside this process is out of
// scope by the same decision that accepts a global lock over path locking.
func renameNoReplace(src, dst string) error {
	return renameChecked(src, dst)
}
