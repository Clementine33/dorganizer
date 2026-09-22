package httpapi

import (
	"net/http"
	"strconv"

	"github.com/onsei/organizer/backend/internal/conversion"
	"github.com/onsei/organizer/backend/internal/workset"
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

func toCustomTagItem(r conversion.ClassifierTag) classifierCustomTagItem {
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
	customTags, err := s.deps.Catalog.Tags()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load classifier tags")
		return
	}

	customItems := make([]classifierCustomTagItem, 0, len(customTags))
	for _, r := range customTags {
		customItems = append(customItems, toCustomTagItem(r))
	}

	writeJSON(w, http.StatusOK, classifierTagLibraryResponse{
		DefaultTags: s.deps.Catalog.DefaultTags(),
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
	created, err := s.deps.Catalog.AddTag(req.Tag)
	if err != nil {
		if _, refused := workset.AsError(err); refused {
			writeWorksetError(w, err)
			return
		}
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
	// A tag that is not in the library is the only outcome the caller can act
	// on, and it is what a failed removal means to them either way.
	if err := s.deps.Catalog.DeleteTag(id); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
