package scan

import (
	"context"
	"fmt"
	"os"

	"github.com/onsei/organizer/backend/internal/services/scanner"
)

// RefreshMember re-scans one member directory subtree into the stored
// inventory: the workbench's "current files" page refreshes a member this way
// before it allows any modification, and a file operation refreshes what it
// touched. The scan is scoped to the member but keyed to the library root, so
// the rows it writes keep their owning root and the stale cleanup inside the
// scope removes what the disk no longer has.
//
// It is the disk → inventory seam, not a scheduled job: the caller decides
// when it runs, and it reports the scan's failure rather than swallowing it.
func (s *serviceImpl) RefreshMember(ctx context.Context, folderPath, rootPath string) error {
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
	svc := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(s.repo))
	if _, scanErr := svc.ScanFolderCtx(ctx, folderPath, rootPath); scanErr != nil {
		return NewError(ErrKindInternal, "SCAN_FAILED", "failed to refresh the member inventory", scanErr)
	}
	return nil
}
