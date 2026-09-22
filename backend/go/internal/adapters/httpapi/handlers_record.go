package httpapi

import (
	"net/http"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// recordCreateRequest is the PUT
// /api/v1/libraries/{id}/operations/{type}/current payload. folder_paths are
// library-relative member directories — the same identity the overview lists —
// and expected_current_id is the record the caller saw as current (absent when
// it saw none), which is what turns a concurrent replace into a conflict
// instead of an overwrite.
type recordCreateRequest struct {
	FolderPaths       []string `json:"folder_paths"`
	ExpectedCurrentID string   `json:"expected_current_id"`
	Title             string   `json:"title"`
}

// skippedFolderResponse is one selected directory that did not become a
// member, with the reason the scope is narrower than the selection.
type skippedFolderResponse struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type recordCreateResponse struct {
	Workset  worksetResponse         `json:"workset"`
	Created  bool                    `json:"created"`
	Recorded int                     `json:"recorded"`
	Skipped  []skippedFolderResponse `json:"skipped"`
}

// currentRecordResponse carries zero or one record: a library that has no
// record for the operation answers with a null record and a 200, which is the
// ordinary "choose folders first" state, not an error.
type currentRecordResponse struct {
	Workset *worksetResponse `json:"workset"`
}

// getCurrentRecord handles GET /api/v1/libraries/{id}/operations/{type}/current.
//
// A library that has no record for the operation answers with a null record
// and a 200 — "choose folders first" is an ordinary state. A library that does
// not exist is a 404: the record of a deleted library is gone with it, and
// answering "no record" would hide that.
func (s *Server) getCurrentRecord(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	if _, ok := s.library(w, r); !ok {
		return
	}
	view, err := svc.GetCurrentWorkset(r.Context(), r.PathValue("id"), r.PathValue("type"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	if view == nil {
		writeJSON(w, http.StatusOK, currentRecordResponse{})
		return
	}
	out := toWorksetResponse(view)
	writeJSON(w, http.StatusOK, currentRecordResponse{Workset: &out})
}

// putCurrentRecord handles PUT
// /api/v1/libraries/{id}/operations/{type}/current: it creates the current
// record of the (library, operation) pair, replacing the record the caller saw
// as current in the same transaction. An idempotency key makes a retried
// request answer with the record it already created; a key reused for a
// different request, a record that changed meanwhile, or a record with a live
// session is a conflict, and the old record is kept either way.
func (s *Server) putCurrentRecord(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req recordCreateRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid record payload")
		return
	}
	result, err := svc.CreateCurrentWorkset(r.Context(), worksetusecase.CreateCurrentRequest{
		LibraryID:         r.PathValue("id"),
		OperationType:     r.PathValue("type"),
		Title:             req.Title,
		FolderPaths:       req.FolderPaths,
		ExpectedCurrentID: req.ExpectedCurrentID,
		IdempotencyKey:    r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	skipped := make([]skippedFolderResponse, 0, len(result.Skipped))
	for _, item := range result.Skipped {
		skipped = append(skipped, skippedFolderResponse{Path: item.Path, Reason: item.Reason})
	}
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, recordCreateResponse{
		Workset:  toWorksetResponse(result.Workset),
		Created:  result.Created,
		Recorded: result.Recorded,
		Skipped:  skipped,
	})
}
