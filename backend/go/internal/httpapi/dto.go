package httpapi

import (
	"net/http"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// errorResponse is the standard API error envelope: {"code":"...","message":"..."}
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError aborts the request with the given status and envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}

// healthResponse is the payload of GET /api/v1/health.
type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

// libraryResponse mirrors the repo Library type with snake_case JSON.
type libraryResponse struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	RootPath       string     `json:"root_path"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastScanAt     *time.Time `json:"last_scan_at"`
	LastScanStatus string     `json:"last_scan_status"`
	LastScanError  string     `json:"last_scan_error"`
}

func toLibraryResponse(l *sqlite.Library) libraryResponse {
	return libraryResponse{
		ID:             l.ID,
		Name:           l.Name,
		RootPath:       l.RootPath,
		CreatedAt:      l.CreatedAt,
		UpdatedAt:      l.UpdatedAt,
		LastScanAt:     l.LastScanAt,
		LastScanStatus: l.LastScanStatus,
		LastScanError:  l.LastScanError,
	}
}

// libraryCreateRequest is the POST /api/v1/libraries payload.
type libraryCreateRequest struct {
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
}

// libraryPatchRequest is the PATCH /api/v1/libraries/:id payload. Pointer
// fields distinguish absent fields from explicit empty values.
type libraryPatchRequest struct {
	Name     *string `json:"name"`
	RootPath *string `json:"root_path"`
}
