package inventory

import (
	"context"
	"fmt"
	"os"
)

// RefreshMember re-scans one member directory subtree into the stored
// inventory: the workbench's "current files" page refreshes a member this way
// before it allows any modification, and a file operation refreshes what it
// touched. The scan is scoped to the member but keyed to the library root, so
// the rows it writes keep their owning root and the stale cleanup inside the
// scope removes what the disk no longer has.
//
// It takes no admission: every caller either already holds the slot (direct
// file management refreshes what it wrote) or takes it through
// AdmitMemberRefresh. Taking it here would refuse the refresh of a file
// operation that legitimately holds the slot.
//
// It is the disk → inventory seam, not a scheduled job: the caller decides
// when it runs, and it reports the scan's failure rather than swallowing it.
func (s *service) RefreshMember(ctx context.Context, folderPath, rootPath string) error {
	if folderPath == "" || rootPath == "" {
		return NewError(
			ErrKindInvalidArgument,
			"MEMBER_PATH_REQUIRED",
			"both the member directory and the library root are required",
			nil,
		)
	}
	info, err := os.Stat(folderPath)
	if err != nil {
		return NewError(
			ErrKindInvalidArgument,
			"MEMBER_PATH_NOT_FOUND",
			fmt.Sprintf("member directory not found: %s", folderPath),
			err,
		)
	}
	if !info.IsDir() {
		return NewError(
			ErrKindInvalidArgument,
			"MEMBER_PATH_NOT_DIRECTORY",
			fmt.Sprintf("member path is not a directory: %s", folderPath),
			nil,
		)
	}
	if _, scanErr := s.pipeline.ScanFolderCtx(ctx, folderPath, rootPath); scanErr != nil {
		return NewError(ErrKindInternal, "SCAN_FAILED", "failed to refresh the member inventory", scanErr)
	}
	return nil
}
