package httpapi

import (
	"net/http"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// ==================== DTOs ====================

// draftDocumentRequest is the PUT .../operations/{type}/draft payload: the four
// common setting groups plus the sparse member records. Unoverridden units are
// absent from member records; null is never the way to express "clear".
type draftDocumentRequest struct {
	SchemaVersion  int                      `json:"schema_version"`
	Mode           string                   `json:"mode"`
	ClassifierTags []string                 `json:"classifier_tags"`
	Matched        reconcile.DesiredProfile `json:"matched"`
	Unmatched      reconcile.DesiredProfile `json:"unmatched"`
	Members        []draftMemberRequest     `json:"members"`
}

type draftMemberRequest struct {
	MemberID  string                 `json:"member_id"`
	Excluded  bool                   `json:"excluded"`
	Overrides *draftOverridesRequest `json:"overrides"`
}

type draftOverridesRequest struct {
	Mode           *string                   `json:"mode"`
	ClassifierTags *[]string                 `json:"classifier_tags"`
	Matched        *reconcile.DesiredProfile `json:"matched"`
	Unmatched      *reconcile.DesiredProfile `json:"unmatched"`
}

// draftResponse is the persisted sparse draft. Version is the OPERATION version
// — the If-Match authority for the next save; there is no separate draft
// counter.
type draftResponse struct {
	WorksetID     string               `json:"workset_id"`
	OperationType string               `json:"operation_type"`
	Version       int                  `json:"version"`
	SchemaVersion int                  `json:"schema_version"`
	Document      draftDocumentRequest `json:"document"`
	UpdatedAt     string               `json:"updated_at"`
}

// revisionListResponse is the revision history payload. NextBeforeIndex is the
// keyset cursor for the next (older) page; 0 means the page reached the oldest
// revision.
type revisionListResponse struct {
	Revisions       []currentRevisionResponse `json:"revisions"`
	NextBeforeIndex int                       `json:"next_before_index"`
}

// revisionMemberResponse is one member of a frozen revision: its effective
// settings plus, per unit, whether they came from the member or the common
// settings. Equal values with different sources stay distinguishable.
type revisionMemberResponse struct {
	MemberID   string            `json:"member_id"`
	MemberName string            `json:"member_name"`
	FolderPath string            `json:"folder_path"`
	Excluded   bool              `json:"excluded"`
	Effective  reconcile.Policy  `json:"effective"`
	Sources    map[string]string `json:"sources"`
}

func toDraftResponse(d *worksetusecase.Draft) draftResponse {
	return draftResponse{
		WorksetID:     d.WorksetID,
		OperationType: d.OperationType,
		Version:       d.Version,
		SchemaVersion: d.SchemaVersion,
		Document:      toDraftDocument(d.Document),
		UpdatedAt:     d.UpdatedAt.UTC().Format(timeFormatJSON),
	}
}

func toDraftDocument(doc *worksetusecase.DraftDoc) draftDocumentRequest {
	out := draftDocumentRequest{
		SchemaVersion:  doc.SchemaVersion,
		Mode:           doc.Mode,
		ClassifierTags: doc.ClassifierTags,
		Matched:        doc.Matched,
		Unmatched:      doc.Unmatched,
		Members:        []draftMemberRequest{},
	}
	if out.ClassifierTags == nil {
		out.ClassifierTags = []string{}
	}
	for _, m := range doc.Members {
		req := draftMemberRequest{MemberID: m.MemberID, Excluded: m.Excluded}
		if m.Overrides != nil {
			req.Overrides = &draftOverridesRequest{
				Mode:           m.Overrides.Mode,
				ClassifierTags: m.Overrides.ClassifierTags,
				Matched:        m.Overrides.Matched,
				Unmatched:      m.Overrides.Unmatched,
			}
		}
		out.Members = append(out.Members, req)
	}
	return out
}

func toDraftDoc(req draftDocumentRequest) *worksetusecase.DraftDoc {
	doc := &worksetusecase.DraftDoc{
		SchemaVersion:  req.SchemaVersion,
		Mode:           req.Mode,
		ClassifierTags: req.ClassifierTags,
		Matched:        req.Matched,
		Unmatched:      req.Unmatched,
	}
	for _, m := range req.Members {
		rec := worksetusecase.DraftMember{MemberID: m.MemberID, Excluded: m.Excluded}
		if m.Overrides != nil {
			rec.Overrides = &worksetusecase.OverrideSet{
				Mode:           m.Overrides.Mode,
				ClassifierTags: m.Overrides.ClassifierTags,
				Matched:        m.Overrides.Matched,
				Unmatched:      m.Overrides.Unmatched,
			}
		}
		doc.Members = append(doc.Members, rec)
	}
	return doc
}

// ==================== Handlers ====================

// getOperation handles GET /api/v1/worksets/{id}/operations/{type}.
func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	view, err := svc.GetOperation(r.Context(), r.PathValue("id"), r.PathValue("type"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOperationResponse(view))
}

// getOperationDraft handles GET /api/v1/worksets/{id}/operations/{type}/draft.
func (s *Server) getOperationDraft(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	d, err := svc.GetDraft(r.Context(), r.PathValue("id"), r.PathValue("type"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

// putOperationDraft handles PUT /api/v1/worksets/{id}/operations/{type}/draft
// (full replacement of the sparse document, If-Match on the operation version).
func (s *Server) putOperationDraft(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req draftDocumentRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid draft payload")
		return
	}
	version, valid := ifMatchVersion(r)
	if !valid {
		writeError(
			w,
			http.StatusBadRequest,
			"VERSION_REQUIRED",
			"If-Match header with the operation version is required",
		)
		return
	}
	view, err := svc.SaveDraft(r.Context(), r.PathValue("id"), r.PathValue("type"), worksetusecase.SaveDraftRequest{
		Document:       toDraftDoc(req),
		IfMatchVersion: version,
	})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOperationResponse(view))
}

// listRevisions handles GET /api/v1/worksets/{id}/operations/{type}/revisions.
func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	limit := min(queryInt(r, "limit", 50), 200)
	before := queryInt(r, "before_index", 0)
	result, err := svc.ListRevisions(r.Context(), r.PathValue("id"), r.PathValue("type"), before, limit)
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	out := make([]currentRevisionResponse, 0, len(result.Revisions))
	for _, rv := range result.Revisions {
		out = append(out, *toCurrentRevisionResponse(rv))
	}
	writeJSON(w, http.StatusOK, revisionListResponse{Revisions: out, NextBeforeIndex: result.NextBeforeIndex})
}

// getRevision handles
// GET /api/v1/worksets/{id}/operations/{type}/revisions/{planId}.
func (s *Server) getRevision(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	rv, err := svc.GetRevision(r.Context(), r.PathValue("id"), r.PathValue("type"), r.PathValue("planId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	// A revision over zero components would otherwise marshal as null; the
	// frontend contract is always an array.
	componentRoots := rv.ComponentRoots
	if componentRoots == nil {
		componentRoots = []worksetusecase.ComponentRootRef{}
	}
	confirmation, confErr := svc.GetConfirmation(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("type"),
		r.PathValue("planId"),
	)
	if confErr != nil {
		writeWorksetError(w, confErr)
		return
	}
	members := make([]revisionMemberResponse, 0, len(rv.Members))
	for _, m := range rv.Members {
		members = append(members, revisionMemberResponse{
			MemberID:   m.MemberID,
			MemberName: m.MemberName,
			FolderPath: m.FolderPath,
			Excluded:   m.Excluded,
			Effective:  m.Policy,
			Sources:    m.Sources,
		})
	}
	writeJSON(w, http.StatusOK, struct {
		PlanID         string                            `json:"plan_id"`
		RevisionIndex  int                               `json:"revision_index"`
		CreatedAt      string                            `json:"created_at"`
		Counts         revisionCountsResponse            `json:"counts"`
		Members        []revisionMemberResponse          `json:"members"`
		Roots          []rootValidationResponse          `json:"roots"`
		ComponentRoots []worksetusecase.ComponentRootRef `json:"component_roots"`
		Confirmation   worksetusecase.ConfirmationView   `json:"confirmation"`
		Workflow       workflowPlanResponse              `json:"workflow"`
	}{
		PlanID:        rv.PlanID,
		RevisionIndex: rv.RevisionIndex,
		CreatedAt:     rv.CreatedAt.UTC().Format(timeFormatJSON),
		Counts: revisionCountsResponse{
			Members:      rv.Counts.Members,
			Changed:      rv.Counts.Changed,
			UnmetTargets: rv.Counts.UnmetTargets,
			Blocked:      rv.Counts.Blocked,
			Unchanged:    rv.Counts.Unchanged,
		},
		Members:        members,
		Roots:          toRoots(rv.Roots),
		ComponentRoots: componentRoots,
		Confirmation:   *confirmation,
		Workflow:       toWorkflowPlanResponse(rv.Workflow),
	})
}

// confirmRevision handles
// POST /api/v1/worksets/{id}/operations/{type}/revisions/{planId}/confirmation.
// If-Match guards the authoritative checks; 201 = first confirmation, 200 =
// idempotent repeat.
func (s *Server) confirmRevision(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	version, valid := ifMatchVersion(r)
	if !valid {
		writeError(
			w,
			http.StatusBadRequest,
			"VERSION_REQUIRED",
			"If-Match header with the operation version is required",
		)
		return
	}
	res, err := svc.ConfirmRevision(r.Context(), r.PathValue("id"), r.PathValue("type"), r.PathValue("planId"),
		worksetusecase.ConfirmRequest{IfMatchVersion: version})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	status := http.StatusCreated
	if !res.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, res.Confirmation)
}
