package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// timeFormatJSON is the RFC3339 format used for all JSON timestamps.
const timeFormatJSON = time.RFC3339Nano

// queryInt parses an integer query parameter with a fallback default.
func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// ifMatchVersion parses the If-Match header as an integer version.
func ifMatchVersion(r *http.Request) (int, bool) {
	v := r.Header.Get("If-Match")
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// rootValidationResponse is per-root validation of a revision snapshot.
type rootValidationResponse struct {
	RootIndex            int    `json:"root_index"`
	RootPath             string `json:"root_path"`
	RootStatus           string `json:"root_status"`
	RootErrorCode        string `json:"root_error_code"`
	RootErrorMessage     string `json:"root_error_message"`
	Stale                bool   `json:"stale"`
	InventoryFingerprint string `json:"inventory_fingerprint"`
	EntryCount           int    `json:"entry_count"`
}

func toRoots(roots []worksetusecase.RootValidation) []rootValidationResponse {
	out := make([]rootValidationResponse, 0, len(roots))
	for _, r := range roots {
		out = append(out, rootValidationResponse{
			RootIndex:            r.RootIndex,
			RootPath:             r.RootPath,
			RootStatus:           r.RootStatus,
			RootErrorCode:        r.RootErrorCode,
			RootErrorMessage:     r.RootErrorMessage,
			Stale:                r.Stale,
			InventoryFingerprint: r.InventoryFingerprint,
			EntryCount:           r.EntryCount,
		})
	}
	return out
}

// writeWorksetError maps workset usecase errors to the standard envelope.
func writeWorksetError(w http.ResponseWriter, err error) {
	if werr, ok := worksetusecase.AsError(err); ok {
		status := http.StatusInternalServerError
		switch werr.Kind {
		case worksetusecase.ErrKindInvalidArgument:
			status = http.StatusBadRequest
		case worksetusecase.ErrKindNotFound:
			status = http.StatusNotFound
		case worksetusecase.ErrKindConflict:
			status = http.StatusConflict
		case worksetusecase.ErrKindPrecondition:
			status = http.StatusPreconditionFailed
		}
		if len(werr.Details) > 0 {
			writeJSON(w, status, errorResponse{
				Code: werr.Code, Message: werr.Message, Details: werr.Details,
			})
			return
		}
		writeError(w, status, werr.Code, werr.Message)
		return
	}
	if errors.Is(err, sqlite.ErrWorksetNotFound) {
		writeError(w, http.StatusNotFound, "WORKSET_NOT_FOUND", "workset not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", "workset operation failed")
}
