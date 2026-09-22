package httpapi

import (
	"errors"
	"net/http"

	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/library"
)

// library loads the library of the request path.
func (s *Server) library(w http.ResponseWriter, r *http.Request) (*library.Library, bool) {
	lib, err := s.deps.Library.Get(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, library.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
		return nil, false
	}
	return lib, true
}

func (s *Server) listLibraries(w http.ResponseWriter, _ *http.Request) {
	libs, err := s.deps.Library.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list libraries")
		return
	}
	out := make([]libraryResponse, 0, len(libs))
	for _, l := range libs {
		out = append(out, toLibraryResponse(l))
	}
	writeJSON(w, http.StatusOK, struct {
		Libraries []libraryResponse `json:"libraries"`
	}{Libraries: out})
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var req libraryCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid library payload")
		return
	}
	lib, err := s.deps.Library.Create(req.Name, req.RootPath)
	if err != nil {
		if errors.Is(err, library.ErrLibraryExists) {
			writeError(w, http.StatusConflict, "LIBRARY_EXISTS", "a library with this root path already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create library")
		return
	}
	writeJSON(w, http.StatusCreated, toLibraryResponse(lib))
}

func (s *Server) getLibrary(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.library(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toLibraryResponse(lib))
}

// patchLibrary edits one library. Only the fields the request carries change;
// a root change is a path-rewriting action and an admission refusal is one of
// its legitimate answers (ADR 0002 §2).
func (s *Server) patchLibrary(w http.ResponseWriter, r *http.Request) {
	var req libraryPatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid library payload")
		return
	}

	updated, err := s.deps.Library.Patch(r.PathValue("id"), req.Name, req.RootPath)
	if err != nil {
		switch {
		case admission.IsBusy(err):
			writeBusyError(w, err)
		case errors.Is(err, library.ErrLibraryExists):
			writeError(w, http.StatusConflict, "LIBRARY_EXISTS", "a library with this root path already exists")
		case errors.Is(err, library.ErrLibraryNotFound):
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
		case errors.Is(err, library.ErrLibraryHasWorksets):
			writeError(
				w,
				http.StatusConflict,
				"LIBRARY_HAS_WORKSETS",
				"cannot change the library root while a record is linked; delete the library (its record goes with it) and re-create it at the new root",
			)
		default:
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to update library")
		}
		return
	}
	writeJSON(w, http.StatusOK, toLibraryResponse(updated))
}

// deleteLibrary removes a library entry together with its record; the media and
// the recovery directory on disk stay. The admission slot is the library
// service's to take, so a busy process answers BUSY before this id is even
// looked up (ADR 0002 §2; ADR 0001 §5).
func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Library.Delete(r.PathValue("id"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case admission.IsBusy(err):
		writeBusyError(w, err)
	case errors.Is(err, library.ErrLibraryNotFound):
		writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
	case errors.Is(err, library.ErrGenerationInProgress):
		writeError(
			w,
			http.StatusConflict,
			"GENERATION_IN_PROGRESS",
			"cancel active generations before deleting the library",
		)
	case errors.Is(err, library.ErrExecutionInProgress):
		writeError(
			w,
			http.StatusConflict,
			"EXECUTION_IN_PROGRESS",
			"cancel active executions before deleting the library",
		)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to delete library")
	}
}
