package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// sqlitePolicySlot aliases the repo row type so the mapper stays terse.
type sqlitePolicySlot = sqlite.PolicySlotRow

// ==================== policy slots ====================
//
// The three fixed global policy slots. Slots are reusable templates only:
// applying one copies its policy into a workset draft as an inline snapshot,
// so slot edits never change existing drafts or revisions.

type policySlotResponse struct {
	Slot      int             `json:"slot"`
	Name      string          `json:"name"`
	Policy    json.RawMessage `json:"policy"`
	UpdatedAt string          `json:"updated_at,omitempty"`
}

type policySlotListResponse struct {
	Slots []policySlotResponse `json:"slots"`
}

type policySlotPutRequest struct {
	Name   string          `json:"name"`
	Policy json.RawMessage `json:"policy"`
}

// toPolicySlotResponse marshals one slot; an empty slot carries policy:null.
func toPolicySlotResponse(slot *sqlitePolicySlot) policySlotResponse {
	out := policySlotResponse{Slot: slot.SlotIndex, Name: slot.Name}
	if slot.PolicyJSON != "" {
		out.Policy = json.RawMessage(slot.PolicyJSON)
	} else {
		out.Policy = json.RawMessage("null")
	}
	if !slot.UpdatedAt.IsZero() {
		out.UpdatedAt = slot.UpdatedAt.UTC().Format(timeFormatJSON)
	}
	return out
}

// listPolicySlots handles GET /api/v1/policy-slots.
func (s *Server) listPolicySlots(w http.ResponseWriter, _ *http.Request) {
	slots, err := s.deps.Repo.GetPolicySlots()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load policy slots")
		return
	}
	out := make([]policySlotResponse, 0, len(slots))
	for _, slot := range slots {
		out = append(out, toPolicySlotResponse(slot))
	}
	writeJSON(w, http.StatusOK, policySlotListResponse{Slots: out})
}

// putPolicySlot handles PUT /api/v1/policy-slots/{slot}. The request must
// carry a non-empty name (1..120 runes) and a fully valid policy; Go
// classifier resolution is the validation authority.
func (s *Server) putPolicySlot(w http.ResponseWriter, r *http.Request) {
	slotIndex, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil || slotIndex < 1 || slotIndex > 3 {
		writeError(w, http.StatusBadRequest, "INVALID_SLOT", "policy slot must be 1, 2 or 3")
		return
	}
	var req policySlotPutRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid policy slot payload")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		writeError(w, http.StatusBadRequest, "INVALID_SLOT_NAME", "slot name must be 1-120 characters")
		return
	}
	if len(req.Policy) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_POLICY", "policy is required")
		return
	}
	policy, err := parseInlinePolicy(req.Policy)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_POLICY", err.Error())
		return
	}
	if validateErr := reconcile.ValidatePolicy(policy); validateErr != nil {
		writeError(w, http.StatusBadRequest, "INVALID_POLICY", validateErr.Error())
		return
	}
	if _, resolveErr := reconcile.ResolveClassifier(policy.ClassifierTags); resolveErr != nil {
		writeError(w, http.StatusBadRequest, "INVALID_POLICY", resolveErr.Error())
		return
	}
	if updateErr := s.deps.Repo.UpdatePolicySlot(slotIndex, name, string(req.Policy)); updateErr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to update policy slot")
		return
	}
	slot, err := s.deps.Repo.GetPolicySlot(slotIndex)
	if err != nil || slot == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load policy slot")
		return
	}
	writeJSON(w, http.StatusOK, toPolicySlotResponse(slot))
}
