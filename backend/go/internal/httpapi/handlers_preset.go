package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// ==================== workflow presets ====================
//
// Read-only registry of the compiled-in policy presets. The frontend uses
// this endpoint as the single source of truth for preset templates: the
// resolved immutable policy lets it present a preset as an editable inline
// form ("template to custom") without duplicating policy constants.

type presetResponse struct {
	Name    string          `json:"name"`
	Version int             `json:"version"`
	Policy  json.RawMessage `json:"policy"`
}

type presetListResponse struct {
	Presets []presetResponse `json:"presets"`
}

// toPresetPolicyJSON marshals a resolved reconcile.Policy using the same wire
// shape as the draft path's reconcilePolicy mirror, so a preset converted to
// inline by the frontend round-trips through SaveDraft unchanged.
func toPresetPolicyJSON(p reconcile.Policy) json.RawMessage {
	out := reconcilePolicy{
		SchemaVersion: p.SchemaVersion,
		Classifier: reconcilePolicyClassifier{
			Name:    p.Classifier.Name,
			Version: p.Classifier.Version,
		},
	}
	if p.Matched.Lossless != nil {
		out.Matched.Lossless = &reconcileOutputSpec{
			Codec:   string(p.Matched.Lossless.Codec),
			Quality: qualityToWire(p.Matched.Lossless.Quality),
		}
	}
	if p.Matched.Encoded != nil {
		out.Matched.Encoded = &reconcileOutputSpec{
			Codec:   string(p.Matched.Encoded.Codec),
			Quality: qualityToWire(p.Matched.Encoded.Quality),
		}
	}
	if p.Unmatched.Lossless != nil {
		out.Unmatched.Lossless = &reconcileOutputSpec{
			Codec:   string(p.Unmatched.Lossless.Codec),
			Quality: qualityToWire(p.Unmatched.Lossless.Quality),
		}
	}
	if p.Unmatched.Encoded != nil {
		out.Unmatched.Encoded = &reconcileOutputSpec{
			Codec:   string(p.Unmatched.Encoded.Codec),
			Quality: qualityToWire(p.Unmatched.Encoded.Quality),
		}
	}
	return rawJSON(out)
}

func qualityToWire(q *reconcile.Quality) *qualitySpec {
	if q == nil {
		return nil
	}
	return &qualitySpec{Kind: string(q.Kind), Bitrate: q.Bitrate}
}

// listWorkflowPresets handles GET /api/v1/workflow-presets.
func (s *Server) listWorkflowPresets(w http.ResponseWriter, _ *http.Request) {
	out := presetListResponse{Presets: make([]presetResponse, 0, len(reconcile.Presets))}
	for _, p := range reconcile.Presets {
		out.Presets = append(out.Presets, presetResponse{
			Name:    p.Name,
			Version: p.Version,
			Policy:  toPresetPolicyJSON(p.Policy),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
