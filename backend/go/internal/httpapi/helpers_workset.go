package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
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

// planSummaryResponse mirrors the plan usecase summary.
type planSummaryResponse struct {
	OperationCount  int    `json:"operation_count"`
	ErrorCount      int    `json:"error_count"`
	TotalCount      int    `json:"total_count"`
	ActionableCount int    `json:"actionable_count"`
	SummaryReason   string `json:"summary_reason"`
}

// workflowStepResponse is the layered review payload of one step.
type workflowStepResponse struct {
	StepType   string            `json:"step_type"`
	StepIndex  int               `json:"step_index"`
	Status     string            `json:"status"`
	Policy     json.RawMessage   `json:"policy"`
	PolicyHash string            `json:"policy_hash"`
	Classifier json.RawMessage   `json:"classifier"`
	Summary    json.RawMessage   `json:"summary"`
	Components []json.RawMessage `json:"components"`
}

// workflowPlanResponse is the review shape of one revision's workflow snapshot.
type workflowPlanResponse struct {
	PlanID        string                 `json:"plan_id"`
	SnapshotToken string                 `json:"snapshot_token"`
	RootPath      string                 `json:"root_path"`
	PlanKind      string                 `json:"plan_kind"`
	Summary       planSummaryResponse    `json:"summary"`
	Steps         []workflowStepResponse `json:"steps"`
}

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

// toWorkflowPlanResponse maps a plan usecase response to the HTTP review
// shape, keeping component outcomes as raw JSON snapshots so the payload
// agrees byte-for-byte with the persisted outcome.
func toWorkflowPlanResponse(resp planusecase.Response) workflowPlanResponse {
	out := workflowPlanResponse{
		PlanID:        resp.PlanID,
		SnapshotToken: resp.SnapshotToken,
		RootPath:      resp.RootPath,
		PlanKind:      resp.PlanKind,
		Summary: planSummaryResponse{
			OperationCount:  resp.Summary.OperationCount,
			ErrorCount:      resp.Summary.ErrorCount,
			TotalCount:      resp.Summary.TotalCount,
			ActionableCount: resp.Summary.ActionableCount,
			SummaryReason:   resp.Summary.SummaryReason,
		},
	}
	for _, step := range resp.Steps {
		components := make([]json.RawMessage, 0, len(step.Components))
		for _, c := range step.Components {
			components = append(components, rawJSON(c))
		}
		out.Steps = append(out.Steps, workflowStepResponse{
			StepType:   step.StepType,
			StepIndex:  step.StepIndex,
			Status:     step.Status,
			Policy:     rawJSON(step.Policy),
			PolicyHash: step.PolicyHash,
			Classifier: rawJSON(struct {
				Tags []string `json:"tags"`
				Hash string   `json:"hash"`
			}{step.Classifier.Tags, step.Classifier.Hash}),
			Summary:    rawJSON(step.Summary),
			Components: components,
		})
	}
	return out
}
