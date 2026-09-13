package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// ==================== Classifier Tag Library ====================

type classifierCustomTagItem struct {
	ID        int64  `json:"id"`
	Tag       string `json:"tag"`
	CreatedAt string `json:"created_at,omitempty"`
}

type classifierTagLibraryResponse struct {
	DefaultTags []string                  `json:"default_tags"`
	CustomTags  []classifierCustomTagItem `json:"custom_tags"`
}

type classifierTagCreateRequest struct {
	Tag string `json:"tag"`
}

func toCustomTagItem(r sqlite.ClassifierTagRow) classifierCustomTagItem {
	item := classifierCustomTagItem{
		ID:  r.ID,
		Tag: r.Tag,
	}
	if !r.CreatedAt.IsZero() {
		item.CreatedAt = r.CreatedAt.UTC().Format(timeFormatJSON)
	}
	return item
}

// listClassifierTags handles GET /api/v1/classifier-tags.
// Returns both the read-only defaults from config.json and the custom tags from SQLite.
func (s *Server) listClassifierTags(w http.ResponseWriter, _ *http.Request) {
	defaults := appconfig.LoadPruneLiteralTags(s.deps.ConfigDir)
	if defaults == nil {
		defaults = []string{}
	}

	customRows, err := s.deps.Repo.GetClassifierTags()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load classifier tags")
		return
	}

	customItems := make([]classifierCustomTagItem, 0, len(customRows))
	for _, r := range customRows {
		customItems = append(customItems, toCustomTagItem(r))
	}

	writeJSON(w, http.StatusOK, classifierTagLibraryResponse{
		DefaultTags: defaults,
		CustomTags:  customItems,
	})
}

// addClassifierTag handles POST /api/v1/classifier-tags.
// Adds a custom tag to the global library.
func (s *Server) addClassifierTag(w http.ResponseWriter, r *http.Request) {
	var req classifierTagCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid tag payload")
		return
	}
	tag := strings.TrimSpace(req.Tag)
	if tag == "" {
		writeError(w, http.StatusBadRequest, "INVALID_TAG", "tag cannot be empty")
		return
	}
	created, err := s.deps.Repo.AddClassifierTag(tag)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to save tag")
		return
	}
	writeJSON(w, http.StatusCreated, toCustomTagItem(*created))
}

// deleteClassifierTag handles DELETE /api/v1/classifier-tags/{id}.
// Removes a custom tag from the global library.
func (s *Server) deleteClassifierTag(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid tag id")
		return
	}
	if err := s.deps.Repo.DeleteClassifierTag(id); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
