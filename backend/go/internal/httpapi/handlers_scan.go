package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
)

// scanRequest is the POST /api/v1/libraries/:id/scans payload. root_path is
// optional; when absent the library's own root path is scanned.
type scanRequest struct {
	RootPath *string `json:"root_path"`
}

// scanEventData is the JSON data of the started/progress/completed/cancelled/
// error SSE events. Fields are snake_case per the API conventions.
type scanEventData struct {
	Stage        string `json:"stage"`
	Message      string `json:"message,omitempty"`
	ScanID       string `json:"scan_id,omitempty"`
	RootPath     string `json:"root_path,omitempty"`
	FilesScanned int    `json:"files_scanned,omitempty"`
	DirsScanned  int    `json:"dirs_scanned,omitempty"`
	Code         string `json:"code,omitempty"`
}

// postLibraryScan streams a scan of the library root over SSE. The handler
// stays synchronous over the request lifecycle: the scan usecase runs with
// r.Context(), so a client disconnect cancels the scan.
func (s *Server) postLibraryScan(w http.ResponseWriter, r *http.Request) {
	lib, err := s.deps.Repo.GetLibrary(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
		return
	}

	var req scanRequest
	if err := decodeJSONAllowEmpty(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid scan payload")
		return
	}
	rootPath := lib.RootPath
	if req.RootPath != nil {
		rootPath = *req.RootPath
		isLibraryRoot := pathnorm.IsWithinRoot(lib.RootPath, rootPath) && pathnorm.IsWithinRoot(rootPath, lib.RootPath)
		if !isLibraryRoot {
			writeError(w, http.StatusBadRequest, "ROOT_PATH_OUTSIDE_LIBRARY", "root_path must match the selected library root")
			return
		}
	}

	// The scan service is optional in Dependencies (nil until wired); guard
	// before streaming so an unwired server reports a terminal error and a
	// failed scan state instead of panicking mid-stream.
	if s.deps.ScanService == nil {
		_ = s.deps.Repo.UpdateLibraryScanState(lib.ID, "failed", "scan service not configured", time.Now())
		writeError(w, http.StatusInternalServerError, "INTERNAL", "scan service not configured")
		return
	}

	sw, err := newSSEWriter(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "streaming not supported")
		return
	}

	// SSE response headers. WriteHeader commits the 200 with the stream headers
	// before the first event is written.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := sw.Send("started", scanEventData{Stage: "scan", Message: "Scanning " + rootPath}); err != nil {
		return
	}

	result, err := s.deps.ScanService.Scan(r.Context(), scanusecase.Request{RootPath: rootPath}, func(ev scanusecase.Event) {
		// The usecase emits started/progress/completed/error internally; only
		// progress is forwarded, the handler owns the terminal events.
		if ev.Type == "progress" {
			_ = sw.Send("progress", scanEventData{
				Stage:        "scan",
				FilesScanned: ev.FilesScanned,
				DirsScanned:  ev.DirsScanned,
			})
		}
	})
	if err != nil {
		now := time.Now()
		if errors.Is(err, context.Canceled) {
			_ = s.deps.Repo.UpdateLibraryScanState(lib.ID, "canceled", "", now)
			_ = sw.Send("cancelled", scanEventData{Stage: "scan", Message: "scan canceled"})
			return
		}
		code, message := "INTERNAL", err.Error()
		if scanErr, ok := scanusecase.AsError(err); ok {
			code, message = scanErr.Code, scanErr.Message
		}
		_ = s.deps.Repo.UpdateLibraryScanState(lib.ID, "failed", message, now)
		_ = sw.Send("error", scanEventData{Stage: "scan", Code: code, Message: message})
		return
	}

	// Record the derived direct-child folders, then the scan outcome, and
	// signal completion.
	if _, err := s.deps.Repo.ReplaceLibraryFolders(lib.ID, result.RootPath); err != nil {
		_ = s.deps.Repo.UpdateLibraryScanState(lib.ID, "failed", err.Error(), time.Now())
		_ = sw.Send("error", scanEventData{Stage: "scan", Code: "INTERNAL", Message: "failed to persist library folders"})
		return
	}
	_ = s.deps.Repo.UpdateLibraryScanState(lib.ID, "completed", "", time.Now())
	_ = sw.Send("completed", scanEventData{
		Stage:        "scan",
		ScanID:       result.ScanID,
		RootPath:     result.RootPath,
		FilesScanned: result.FilesScanned,
	})
}
