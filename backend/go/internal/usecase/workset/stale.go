package workset

import (
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// validateRevision computes the revision-level validation state from the
// persisted per-root fingerprints against the current scanned inventory.
// It returns (stalePtr, validationState):
//   - orphaned worksets: (nil, "unavailable") — there is no live library to
//     validate against;
//   - any root fingerprint/count mismatch (or a missing root): (true, "stale");
//   - otherwise: (false, "valid").
func (s *serviceImpl) validateRevision(w *sqlite.Workset, detail *sqlite.WorkflowPlanDetail) (*bool, string) {
	if w.LibraryID == "" {
		return nil, ValidationUnavailable
	}
	for _, r := range detail.Roots {
		if rootIsStale(s.repo, r) {
			t := true
			return &t, ValidationStale
		}
	}
	f := false
	return &f, ValidationValid
}

// rootIsStale compares one persisted root fingerprint against the current
// scanned inventory using only audio entries (same normalized collection and
// filtering used at planning time). A missing root whose inventory remains
// empty is not stale (it is represented by root_status/SOURCE_MISSING).
// A collection failure is never "valid": fail closed toward stale.
func rootIsStale(repo *sqlite.Repository, r sqlite.WorkflowRootRecord) bool {
	if r.RootPath == "" {
		return false
	}
	entries, err := collectWorkflowEntries(repo, r.RootPath)
	if err != nil {
		return true
	}
	audio := reconcile.AudioEntries(entries)
	digest, count := reconcile.InventoryFingerprint(audio)
	return digest != r.InventoryFingerprint || count != r.EntryCount
}
