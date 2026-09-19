package httpapi

import (
	"errors"
	"net/http"

	"github.com/onsei/organizer/backend/internal/services/fileops"
)

// fileOperationRequest is the POST /api/v1/libraries/{id}/file-operations
// payload. The member is addressed by its library-relative path; the items are
// addressed relative to that member.
type fileOperationRequest struct {
	MemberPath string              `json:"member_path"`
	Operation  string              `json:"operation"`
	Items      []fileOperationItem `json:"items"`
}

// fileOperationItem is one requested change. Name belongs to a rename (a plain
// name, never a path), TargetDir to a move.
type fileOperationItem struct {
	Source    string `json:"source"`
	Name      string `json:"name,omitempty"`
	TargetDir string `json:"target_dir,omitempty"`
}

// applyFileOperation performs direct file management inside one member
// directory: single-item rename, single-item move inside the member, or a
// batch soft delete into the library's recovery directory.
//
// The whole request holds the direct-file-management slot, so it is refused
// while any scan, planning session or execution is running, and it refuses
// those in turn. Direct file management is its own write path with its own
// path, conflict and admission rules (ADR 0007 §4, §5); it is not a plan and
// is not recorded as one.
func (s *Server) applyFileOperation(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.library(w, r)
	if !ok {
		return
	}
	if s.deps.FileOps == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "file operations are not configured")
		return
	}
	var req fileOperationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid file operation payload")
		return
	}
	items := make([]fileops.Item, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, fileops.Item{Source: item.Source, Name: item.Name, TargetDir: item.TargetDir})
	}

	result, err := s.deps.FileOps.Apply(r.Context(), fileops.Request{
		LibraryRoot: lib.RootPath,
		MemberPath:  req.MemberPath,
		Operation:   req.Operation,
		Items:       items,
	})
	if err != nil {
		if fileops.IsBusy(err) {
			writeBusyError(w, err)
			return
		}
		// A refusal of the whole request (an unsupported operation, an
		// unusable member, a malformed path, no items): the member never
		// became a scope, so nothing ran and nothing was written.
		var itemErr *fileops.PathError
		if errors.As(err, &itemErr) && itemErr.Code == fileops.CodeOperationDenied {
			writeError(w, http.StatusBadRequest, "OPERATION_DENIED", itemErr.Message)
			return
		}
		writeError(w, http.StatusBadRequest, "REQUEST_REFUSED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
