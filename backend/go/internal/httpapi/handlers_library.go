package httpapi

import (
	"errors"
	"net/http"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.deps.Repo.ListLibraries()
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
	lib, err := s.deps.Repo.CreateLibrary(req.Name, req.RootPath)
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryExists) {
			writeError(w, http.StatusConflict, "LIBRARY_EXISTS", "a library with this root path already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create library")
		return
	}
	writeJSON(w, http.StatusCreated, toLibraryResponse(lib))
}

func (s *Server) getLibrary(w http.ResponseWriter, r *http.Request) {
	lib, err := s.deps.Repo.GetLibrary(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
		return
	}
	writeJSON(w, http.StatusOK, toLibraryResponse(lib))
}

func (s *Server) patchLibrary(w http.ResponseWriter, r *http.Request) {
	var req libraryPatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid library payload")
		return
	}
	id := r.PathValue("id")

	lib, err := s.deps.Repo.GetLibrary(id)
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
		return
	}

	// PATCH semantics: only the provided fields are applied; the rest are
	// preserved from the current row.
	name, rootPath := lib.Name, lib.RootPath
	if req.Name != nil {
		name = *req.Name
	}
	if req.RootPath != nil {
		rootPath = *req.RootPath
	}

	updated, err := s.deps.Repo.UpdateLibrary(id, name, rootPath)
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryExists) {
			writeError(w, http.StatusConflict, "LIBRARY_EXISTS", "a library with this root path already exists")
			return
		}
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to update library")
		return
	}
	writeJSON(w, http.StatusOK, toLibraryResponse(updated))
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Repo.DeleteLibrary(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to delete library")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
