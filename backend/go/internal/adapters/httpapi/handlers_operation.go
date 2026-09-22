package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
	"github.com/onsei/organizer/backend/internal/workset"
)

// ==================== DTOs ====================

// draftDocumentRequest is the PUT .../operations/{type}/draft payload: the four
// common setting groups plus the sparse member records. Unoverridden units are
// absent from member records; null is never the way to express "clear".
type draftDocumentRequest struct {
	SchemaVersion  int                      `json:"schema_version"`
	Mode           string                   `json:"mode"`
	DeleteMode     string                   `json:"delete_mode,omitempty"`
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

// taskDraftEnvelope is the opaque task envelope of a draft document: the
// generic side names the kind and schema version, the task owns the payload.
type taskDraftEnvelope struct {
	Kind          string          `json:"kind"`
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// draftResponse is the persisted sparse draft. Version is the OPERATION version
// — the If-Match authority for the next save; there is no separate draft
// counter.
type draftResponse struct {
	WorksetID     string            `json:"workset_id"`
	OperationType string            `json:"operation_type"`
	Version       int               `json:"version"`
	Task          taskDraftEnvelope `json:"task"`
	UpdatedAt     string            `json:"updated_at"`
}

// revisionMemberResponse is one member of a frozen revision: its effective
// settings plus, per unit, whether they came from the member or the common
// settings. Equal values with different sources stay distinguishable.
type revisionMemberResponse struct {
	MemberID   string            `json:"member_id"`
	MemberName string            `json:"member_name"`
	FolderPath string            `json:"folder_path"`
	Excluded   bool              `json:"excluded"`
	Effective  json.RawMessage   `json:"effective"`
	Sources    map[string]string `json:"sources"`
}

func toDraftResponse(d *workset.Draft) draftResponse {
	return draftResponse{
		WorksetID:     d.WorksetID,
		OperationType: d.OperationType,
		Version:       d.Version,
		Task: taskDraftEnvelope{
			Kind:          d.OperationType,
			SchemaVersion: d.SchemaVersion,
			Payload:       d.Document,
		},
		UpdatedAt: d.UpdatedAt.UTC().Format(timeFormatJSON),
	}
}

// toDraftPayload encodes the HTTP draft body into the task's opaque payload.
func toDraftPayload(req draftDocumentRequest) json.RawMessage {
	raw, err := json.Marshal(req)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
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
	view, err := svc.SaveDraft(r.Context(), r.PathValue("id"), r.PathValue("type"), workset.SaveDraftRequest{
		Document:       toDraftPayload(req),
		IfMatchVersion: version,
	})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOperationResponse(view))
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
		componentRoots = []workset.ComponentRootRef{}
	}
	members := make([]revisionMemberResponse, 0, len(rv.Members))
	for _, m := range rv.Members {
		members = append(members, revisionMemberResponse{
			MemberID:   m.MemberID,
			MemberName: m.MemberName,
			FolderPath: m.FolderPath,
			Excluded:   m.Excluded,
			Effective:  m.Payload,
			Sources:    m.Sources,
		})
	}
	writeJSON(w, http.StatusOK, struct {
		PlanID         string                     `json:"plan_id"`
		RevisionIndex  int                        `json:"revision_index"`
		CreatedAt      string                     `json:"created_at"`
		RootPath       string                     `json:"root_path"`
		SnapshotToken  string                     `json:"snapshot_token"`
		Status         string                     `json:"status"`
		Summary        planSummaryResponse        `json:"summary"`
		Task           taskEnvelopeResponse       `json:"task"`
		Counts         revisionCountsResponse     `json:"counts"`
		Members        []revisionMemberResponse   `json:"members"`
		Roots          []rootValidationResponse   `json:"roots"`
		ComponentRoots []workset.ComponentRootRef `json:"component_roots"`
		Execution      *workset.ExecutionRef      `json:"execution"`
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
		RootPath:      rv.Plan.RootPath,
		SnapshotToken: rv.Plan.SnapshotToken,
		Status:        rv.Plan.Status,
		Summary: planSummaryResponse{
			OperationCount: rv.Plan.Summary.OperationCount,
			ErrorCount:     rv.Plan.Summary.ErrorCount,
			SummaryReason:  rv.Plan.Summary.SummaryReason,
		},
		Task:           toTaskEnvelope(rv.Plan),
		Members:        members,
		Roots:          toRoots(rv.Roots),
		ComponentRoots: componentRoots,
		Execution:      rv.Execution,
	})
}
