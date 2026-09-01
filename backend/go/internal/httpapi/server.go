package httpapi

import (
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// Dependencies carries the wiring for the HTTP API. ScanService is used by the
// scan route; PlanService by the plan routes; WorksetService by the workset
// routes. Any may be nil until wired, and the handlers guard against that.
type Dependencies struct {
	Repo           *sqlite.Repository
	ConfigDir      string
	Token          string
	CORSOrigins    []string
	Version        string
	ScanService    scanusecase.Service
	PlanService    planusecase.Service
	WorksetService worksetusecase.Service
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
	mux.Handle("GET /api/v1/libraries/{id}/folders", protect(http.HandlerFunc(s.listLibraryFolders)))
	mux.Handle("GET /api/v1/libraries/{id}/folders/{folderId}/tree", protect(http.HandlerFunc(s.getFolderTree)))
	mux.Handle("POST /api/v1/plans", protect(http.HandlerFunc(s.createPlan)))
	mux.Handle("GET /api/v1/plans", protect(http.HandlerFunc(s.listPlans)))
	mux.Handle("GET /api/v1/plans/{id}", protect(http.HandlerFunc(s.getPlanDetail)))
	mux.Handle("GET /api/v1/policy-slots", protect(http.HandlerFunc(s.listPolicySlots)))
	mux.Handle("PUT /api/v1/policy-slots/{slot}", protect(http.HandlerFunc(s.putPolicySlot)))
	mux.Handle("GET /api/v1/classifier-tags", protect(http.HandlerFunc(s.listClassifierTags)))
	mux.Handle("POST /api/v1/classifier-tags", protect(http.HandlerFunc(s.addClassifierTag)))
	mux.Handle("DELETE /api/v1/classifier-tags/{id}", protect(http.HandlerFunc(s.deleteClassifierTag)))
	mux.Handle("POST /api/v1/worksets", protect(http.HandlerFunc(s.createWorkset)))
	mux.Handle("GET /api/v1/worksets", protect(http.HandlerFunc(s.listWorksets)))
	mux.Handle("GET /api/v1/worksets/{id}", protect(http.HandlerFunc(s.getWorkset)))
	mux.Handle("PATCH /api/v1/worksets/{id}", protect(http.HandlerFunc(s.patchWorkset)))
	mux.Handle("GET /api/v1/worksets/{id}/draft", protect(http.HandlerFunc(s.getWorksetDraft)))
	mux.Handle("PUT /api/v1/worksets/{id}/draft", protect(http.HandlerFunc(s.putWorksetDraft)))
	mux.Handle("POST /api/v1/worksets/{id}/revisions", protect(http.HandlerFunc(s.startGeneration)))
	mux.Handle("GET /api/v1/worksets/{id}/revisions", protect(http.HandlerFunc(s.listRevisions)))
	mux.Handle("GET /api/v1/worksets/{id}/revisions/{planId}", protect(http.HandlerFunc(s.getRevision)))
	mux.Handle("GET /api/v1/worksets/{id}/planning-sessions/{genId}", protect(http.HandlerFunc(s.getGeneration)))
	mux.Handle("GET /api/v1/worksets/{id}/planning-sessions/{genId}/events", protect(http.HandlerFunc(s.generationEvents)))
	mux.Handle("POST /api/v1/worksets/{id}/planning-sessions/{genId}/cancel", protect(http.HandlerFunc(s.cancelGeneration)))
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
