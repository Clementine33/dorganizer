package scan

import (
	"context"
	"fmt"
	"os"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/scanner"
)

type serviceImpl struct {
	repo *sqlite.Repository
}

// NewService creates a new scan service backed by the sqlite repository.
func NewService(repo *sqlite.Repository) Service {
	return &serviceImpl{repo: repo}
}

// Scan validates the root path, streams started/progress/completed events via
// emit, and returns the scan result. On failure it emits an error event and
// returns a typed *Error where applicable.
func (s *serviceImpl) Scan(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	if emit == nil {
		emit = func(Event) {}
	}

	if req.RootPath == "" {
		return Result{}, NewError(ErrKindInvalidArgument, "ROOT_PATH_REQUIRED", "root_path is required", nil)
	}

	fi, statErr := os.Stat(req.RootPath)
	if statErr != nil || !fi.IsDir() {
		cause := statErr
		if statErr == nil {
			cause = fmt.Errorf("not a directory: %s", req.RootPath)
		}
		return Result{}, NewError(ErrKindInvalidArgument, "ROOT_PATH_NOT_FOUND", fmt.Sprintf("root_path not found: %s", req.RootPath), cause)
	}

	emit(Event{Type: "started", Stage: "scan", Message: fmt.Sprintf("Scanning %s", req.RootPath)})

	svc := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(s.repo))
	var lastProgress scanner.Progress
	scanID, err := svc.ScanRootCtx(ctx, req.RootPath, scanner.WithProgress(func(p scanner.Progress) {
		lastProgress = p
		emit(Event{Type: "progress", Stage: "scan", FilesScanned: p.FilesScanned, DirsScanned: p.DirsScanned})
	}))
	if err != nil {
		emit(Event{Type: "error", Stage: "scan", Message: fmt.Sprintf("Scan failed: %v", err)})
		return Result{}, err
	}

	emit(Event{
		Type:         "completed",
		Stage:        "scan",
		FilesScanned: lastProgress.FilesScanned,
		DirsScanned:  lastProgress.DirsScanned,
		ScanID:       scanID,
		Message:      fmt.Sprintf("Scan completed (scan ID: %s)", scanID),
	})

	return Result{
		ScanID:       scanID,
		RootPath:     req.RootPath,
		FilesScanned: lastProgress.FilesScanned,
	}, nil
}
