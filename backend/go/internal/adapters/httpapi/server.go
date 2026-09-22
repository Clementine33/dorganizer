package httpapi

import (
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/library"
	"github.com/onsei/organizer/backend/internal/services/fileops"
	"github.com/onsei/organizer/backend/internal/workset"
)

// Dependencies carries the wiring for the HTTP API. Inventory is used by the
// scan and refresh routes; WorksetService by the workset routes. Any may be nil until
// wired, and the handlers guard against that.
type Dependencies struct {
	Repo *sqlite.Repository
	// Library is the media-library business entry: creation, edits, root
	// changes and deletions go through it, so a root change and a deletion
	// take the admission slot without the HTTP layer knowing about it.
	Library        *library.Service
	ConfigDir      string
	Token          string
	CORSOrigins    []string
	Version        string
	Inventory      inventory.Service
	WorksetService workset.Service
	// FileOps applies direct file management inside a member. It and every
	// other path that takes an admission slot own that decision themselves, so
	// the HTTP layer never holds the gate.
	FileOps *fileops.Service
}

type Server struct{ deps Dependencies }

func NewServer(deps Dependencies) http.Handler {
	s := &Server{deps: deps}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	protect := authMiddleware(deps.Token)
	mux.Handle("POST /api/v1/libraries", protect(http.HandlerFunc(s.createLibrary)))
	mux.Handle("GET /api/v1/libraries", protect(http.HandlerFunc(s.listLibraries)))
	mux.Handle("GET /api/v1/libraries/{id}", protect(http.HandlerFunc(s.getLibrary)))
	mux.Handle("PATCH /api/v1/libraries/{id}", protect(http.HandlerFunc(s.patchLibrary)))
	mux.Handle("DELETE /api/v1/libraries/{id}", protect(http.HandlerFunc(s.deleteLibrary)))
	mux.Handle("POST /api/v1/libraries/{id}/scans", protect(http.HandlerFunc(s.postLibraryScan)))
	// The workbench overview lists every direct child directory; a member tree
	// is read by its library-relative path and refreshed by re-scanning that
	// member.
	mux.Handle("GET /api/v1/libraries/{id}/dirs", protect(http.HandlerFunc(s.listLibraryDirs)))
	mux.Handle("GET /api/v1/libraries/{id}/tree", protect(http.HandlerFunc(s.getMemberTree)))
	mux.Handle("POST /api/v1/libraries/{id}/tree/refresh", protect(http.HandlerFunc(s.refreshMemberTree)))
	// Direct file management: the second, explicitly non-plan write path.
	mux.Handle("POST /api/v1/libraries/{id}/file-operations", protect(http.HandlerFunc(s.applyFileOperation)))
	// The current processing record of one (library, operation): at most one
	// exists, and creating it replaces the one the caller saw.
	mux.Handle(
		"GET /api/v1/libraries/{id}/operations/{type}/current",
		protect(http.HandlerFunc(s.getCurrentRecord)),
	)
	mux.Handle(
		"PUT /api/v1/libraries/{id}/operations/{type}/current",
		protect(http.HandlerFunc(s.putCurrentRecord)),
	)
	mux.Handle("GET /api/v1/policy-slots", protect(http.HandlerFunc(s.listPolicySlots)))
	mux.Handle("PUT /api/v1/policy-slots/{slot}", protect(http.HandlerFunc(s.putPolicySlot)))
	mux.Handle("GET /api/v1/classifier-tags", protect(http.HandlerFunc(s.listClassifierTags)))
	mux.Handle("POST /api/v1/classifier-tags", protect(http.HandlerFunc(s.addClassifierTag)))
	mux.Handle("DELETE /api/v1/classifier-tags/{id}", protect(http.HandlerFunc(s.deleteClassifierTag)))
	mux.Handle("GET /api/v1/worksets", protect(http.HandlerFunc(s.listWorksets)))
	mux.Handle("GET /api/v1/worksets/{id}", protect(http.HandlerFunc(s.getWorkset)))
	mux.Handle("PATCH /api/v1/worksets/{id}", protect(http.HandlerFunc(s.patchWorkset)))
	// Workset operations: every mutable planning resource is addressed through
	// its (workset, operation type) ownership. There is no workset-level draft,
	// planning session or revision route, and no revision-history route: a
	// record keeps exactly one plan, the current one.
	mux.Handle("GET /api/v1/worksets/{id}/operations/{type}", protect(http.HandlerFunc(s.getOperation)))
	mux.Handle("GET /api/v1/worksets/{id}/operations/{type}/draft", protect(http.HandlerFunc(s.getOperationDraft)))
	mux.Handle("PUT /api/v1/worksets/{id}/operations/{type}/draft", protect(http.HandlerFunc(s.putOperationDraft)))
	mux.Handle("POST /api/v1/worksets/{id}/operations/{type}/revisions", protect(http.HandlerFunc(s.startGeneration)))
	mux.Handle(
		"GET /api/v1/worksets/{id}/operations/{type}/revisions/{planId}",
		protect(http.HandlerFunc(s.getRevision)),
	)
	// Execution sessions: the only write path to the disk. The session is
	// addressed through its operation, never by a workset-level route.
	mux.Handle(
		"POST /api/v1/worksets/{id}/operations/{type}/revisions/{planId}/executions",
		protect(http.HandlerFunc(s.startExecution)),
	)
	mux.Handle(
		"GET /api/v1/worksets/{id}/operations/{type}/executions/{executionId}",
		protect(http.HandlerFunc(s.getExecution)),
	)
	mux.Handle(
		"GET /api/v1/worksets/{id}/operations/{type}/executions/{executionId}/events",
		protect(http.HandlerFunc(s.executionEvents)),
	)
	mux.Handle(
		"POST /api/v1/worksets/{id}/operations/{type}/executions/{executionId}/cancel",
		protect(http.HandlerFunc(s.cancelExecution)),
	)
	mux.Handle(
		"GET /api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}",
		protect(http.HandlerFunc(s.getGeneration)),
	)
	mux.Handle(
		"GET /api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/events",
		protect(http.HandlerFunc(s.generationEvents)),
	)
	mux.Handle(
		"POST /api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/cancel",
		protect(http.HandlerFunc(s.cancelGeneration)),
	)
	return recoveryMiddleware(corsMiddleware(deps.CORSOrigins)(routingCompatibilityMiddleware(mux)))
}

func routingCompatibilityMiddleware(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath := r.URL.EscapedPath()
		if path.Clean(r.URL.Path) != r.URL.Path || strings.Contains(strings.ToLower(escapedPath), "%2f") {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodHead {
			methods := allowedMethods(mux, r)
			if len(methods) == 0 {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Allow", strings.Join(methods, ", "))
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	methods := make([]string, 0, 4)
	for _, method := range []string{http.MethodDelete, http.MethodGet, http.MethodPatch, http.MethodPost} {
		probe := r.Clone(r.Context())
		probe.Method = method
		_, pattern := mux.Handler(probe)
		if pattern != "" {
			methods = append(methods, method)
		}
	}
	sort.Strings(methods)
	return methods
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	version := s.deps.Version
	if version == "" {
		version = "dev"
	}
	writeJSON(w, http.StatusOK, healthResponse{OK: true, Version: version})
}
