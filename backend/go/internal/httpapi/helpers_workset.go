package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
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

// toGenerationViewResponse converts the usecase generation view.
func toGenerationViewResponse(g *worksetusecase.GenerationView) generationViewResponse {
	out := generationViewResponse{
		GenerationID:   g.GenerationID,
		WorksetID:      g.WorksetID,
		Status:         g.Status,
		TotalRoots:     g.TotalRoots,
		CompletedRoots: g.CompletedRoots,
		CurrentRoot:    g.CurrentRoot,
		ErrorCount:     g.ErrorCount,
		RevisionID:     g.RevisionID,
		ErrorCode:      g.ErrorCode,
		ErrorMessage:   g.ErrorMessage,
	}
	if !g.StartedAt.IsZero() {
		out.StartedAt = g.StartedAt.UTC().Format(timeFormatJSON)
	}
	if !g.FinishedAt.IsZero() {
		out.FinishedAt = g.FinishedAt.UTC().Format(timeFormatJSON)
	}
	if !g.CreatedAt.IsZero() {
		out.CreatedAt = g.CreatedAt.UTC().Format(timeFormatJSON)
	}
	return out
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

// workflowResponse is the draft/workflow JSON payload (mirrors
// workflowRequest of the plan create path, but without the envelope).
type workflowResponse struct {
	SchemaVersion int                   `json:"schema_version"`
	Steps         []workflowStepRequest `json:"steps"`
}

// toWorkflow converts a draft HTTP request into the usecase workflow shape.
func toWorkflow(req workflowRequest) planusecase.Workflow {
	steps := make([]planusecase.WorkflowStep, 0, len(req.Steps))
	for _, s := range req.Steps {
		var inline *reconcilePolicy
		if s.Policy.Kind == "inline" {
			var p reconcilePolicy
			if len(s.Policy.Policy) > 0 {
				_ = json.Unmarshal(s.Policy.Policy, &p)
			}
			inline = &p
		}
		steps = append(steps, planusecase.WorkflowStep{
			StepType: s.StepType,
			Policy: planusecase.PolicySource{
				Kind:          s.Policy.Kind,
				PresetName:    s.Policy.Name,
				PresetVersion: s.Policy.Version,
				InlinePolicy:  inlineToReconcile(inline),
			},
		})
	}
	return planusecase.Workflow{SchemaVersion: req.SchemaVersion, Steps: steps}
}

// reconcilePolicy mirrors reconcile.Policy for JSON round-trips.
type reconcilePolicy struct {
	SchemaVersion int                       `json:"schema_version"`
	Classifier    reconcilePolicyClassifier `json:"classifier"`
	Matched       reconcilePolicyProfile    `json:"matched"`
	Unmatched     reconcilePolicyProfile    `json:"unmatched"`
}

type reconcilePolicyClassifier struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type reconcilePolicyProfile struct {
	Lossless *reconcileOutputSpec `json:"lossless,omitempty"`
	Encoded  *reconcileOutputSpec `json:"encoded,omitempty"`
}

type reconcileOutputSpec struct {
	Codec   string       `json:"codec"`
	Quality *qualitySpec `json:"quality,omitempty"`
}

type qualitySpec struct {
	Kind    string `json:"kind"`
	Bitrate int    `json:"bitrate,omitempty"`
}

func inlineToReconcile(p *reconcilePolicy) *reconcile.Policy {
	if p == nil {
		return nil
	}
	return &reconcile.Policy{
		SchemaVersion: p.SchemaVersion,
		Classifier:    reconcile.ClassifierRef{Name: p.Classifier.Name, Version: p.Classifier.Version},
		Matched: reconcile.DesiredProfile{
			Lossless: specToReconcile(p.Matched.Lossless),
			Encoded:  specToReconcile(p.Matched.Encoded),
		},
		Unmatched: reconcile.DesiredProfile{
			Lossless: specToReconcile(p.Unmatched.Lossless),
			Encoded:  specToReconcile(p.Unmatched.Encoded),
		},
	}
}

func specToReconcile(s *reconcileOutputSpec) *reconcile.AudioOutputSpec {
	if s == nil {
		return nil
	}
	out := &reconcile.AudioOutputSpec{Codec: reconcile.Codec(s.Codec)}
	if s.Quality != nil {
		out.Quality = &reconcile.Quality{Kind: reconcile.QualityKind(s.Quality.Kind), Bitrate: s.Quality.Bitrate}
	}
	return out
}

// toWorkflowResponse converts the persisted draft workflow to its JSON shape.
func toWorkflowResponse(wf planusecase.Workflow) workflowResponse {
	steps := make([]workflowStepRequest, 0, len(wf.Steps))
	for _, s := range wf.Steps {
		req := workflowStepRequest{StepType: s.StepType}
		req.Policy = policySourceRequest{Kind: s.Policy.Kind, Name: s.Policy.PresetName, Version: s.Policy.PresetVersion}
		if s.Policy.InlinePolicy != nil {
			b, _ := json.Marshal(s.Policy.InlinePolicy)
			req.Policy.Policy = b
		}
		steps = append(steps, req)
	}
	return workflowResponse{SchemaVersion: wf.SchemaVersion, Steps: steps}
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
		writeError(w, status, werr.Code, werr.Message)
		return
	}
	if errors.Is(err, sqlite.ErrWorksetNotFound) {
		writeError(w, http.StatusNotFound, "WORKSET_NOT_FOUND", "workset not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", "workset operation failed")
}
