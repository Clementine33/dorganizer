package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/pathnorm"
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

// writeScanAdmissionError maps a refused admission to its envelope: the gate's
// BUSY, or the scan's own code — an active execution refuses a full scan.
func writeScanAdmissionError(w http.ResponseWriter, err error) {
	if admission.IsBusy(err) {
		writeBusyError(w, err)
		return
	}
	if scanErr, ok := inventory.AsError(err); ok {
		writeError(w, http.StatusConflict, scanErr.Code, scanErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to admit the scan")
}

// postLibraryScan streams a scan of the library root over SSE. The handler
// stays synchronous over the request lifecycle: the scan runs with
// r.Context(), so a client disconnect cancels the scan.
//
// Admission is taken here, before the stream is committed: a refusal is a 409
// envelope, never an event inside a response that already answered 200.
func (s *Server) postLibraryScan(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.library(w, r)
	if !ok {
		return
	}

	var req scanRequest
	if decodeErr := decodeJSONAllowEmpty(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid scan payload")
		return
	}
	rootPath := lib.RootPath
	if req.RootPath != nil {
		rootPath = *req.RootPath
		isLibraryRoot := pathnorm.IsWithinRoot(lib.RootPath, rootPath) && pathnorm.IsWithinRoot(rootPath, lib.RootPath)
		if !isLibraryRoot {
			writeError(
				w,
				http.StatusBadRequest,
				"ROOT_PATH_OUTSIDE_LIBRARY",
				"root_path must match the selected library root",
			)
			return
		}
	}

	// The scanning entry is optional in Dependencies (nil until wired); guard
	// before streaming so an unwired server reports a terminal error and a
	// failed scan state instead of panicking mid-stream.
	if s.deps.Inventory == nil {
		_ = s.deps.Library.RecordScanState(lib.ID, "failed", "scan service not configured", time.Now())
		writeError(w, http.StatusInternalServerError, "INTERNAL", "scan service not configured")
		return
	}

	release, err := s.deps.Inventory.AdmitLibraryScan(lib.RootPath)
	if err != nil {
		writeScanAdmissionError(w, err)
		return
	}
	defer release()

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

	if sendErr := sw.Send("started", scanEventData{Stage: "scan", Message: "Scanning " + rootPath}); sendErr != nil {
		return
	}

	result, err := s.deps.Inventory.Scan(
		r.Context(),
		inventory.Request{LibraryID: lib.ID, RootPath: rootPath},
		func(ev inventory.Event) {
			// The entry emits started/progress/completed internally; only
			// progress is forwarded, the handler owns the terminal events.
			if ev.Type == "progress" {
				_ = sw.Send("progress", scanEventData{
					Stage:        "scan",
					FilesScanned: ev.FilesScanned,
					DirsScanned:  ev.DirsScanned,
				})
			}
		},
	)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_ = sw.Send("cancelled", scanEventData{Stage: "scan", Message: "scan canceled"})
			return
		}
		code, message := "INTERNAL", err.Error()
		if scanErr, ok := inventory.AsError(err); ok {
			code, message = scanErr.Code, scanErr.Message
		}
		_ = sw.Send("error", scanEventData{Stage: "scan", Code: code, Message: message})
		return
	}

	// The scanned inventory is the overview's whole input: there is no separate
	// derived folder table to rebuild, which is why a rescan cannot renumber
	// anything the workbench navigates by.
	_ = sw.Send("completed", scanEventData{
		Stage:        "scan",
		ScanID:       result.ScanID,
		RootPath:     result.RootPath,
		FilesScanned: result.FilesScanned,
	})
}
