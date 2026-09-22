package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/onsei/organizer/backend/internal/conversion"
	"github.com/onsei/organizer/backend/internal/workset"
)

// ==================== policy slots ====================
//
// The three fixed global policy slots. Slots are reusable templates only:
// applying one copies its policy into a workset draft as an inline snapshot,
// so slot edits never change existing drafts or revisions. What a slot may
// contain is the conversion catalog's business; this file is the transport.

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
func toPolicySlotResponse(slot *conversion.PolicySlot) policySlotResponse {
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
	slots, err := s.deps.Catalog.Slots()
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

// putPolicySlot handles PUT /api/v1/policy-slots/{slot}. The catalog checks the
// slot, the name and the policy; what it refuses to store is a bad request, and
// anything else is a storage failure.
func (s *Server) putPolicySlot(w http.ResponseWriter, r *http.Request) {
	slotIndex, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SLOT", "policy slot must be 1, 2 or 3")
		return
	}
	var req policySlotPutRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid policy slot payload")
		return
	}
	slot, putErr := s.deps.Catalog.PutSlot(slotIndex, req.Name, req.Policy)
	if putErr != nil {
		// A refusal carries the catalog's own code and message; anything else
		// came from storage and says nothing the caller can act on.
		if _, refused := workset.AsError(putErr); refused {
			writeWorksetError(w, putErr)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to update policy slot")
		return
	}
	writeJSON(w, http.StatusOK, toPolicySlotResponse(slot))
}
