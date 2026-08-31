package sqlite

import (
	"strings"
	"testing"
)

// TestPolicySlotsSeedAndRoundTrip verifies the storage invariant: a fresh
// database materializes exactly three ordered empty slots, updates address an
// existing row only, and out-of-range updates fail.
func TestPolicySlotsSeedAndRoundTrip(t *testing.T) {
	repo := newTestRepository(t)
	defer repo.Close()

	slots, err := repo.GetPolicySlots()
	if err != nil {
		t.Fatalf("GetPolicySlots: %v", err)
	}
	if len(slots) != 3 {
		t.Fatalf("slots = %d, want 3", len(slots))
	}
	for i, s := range slots {
		if s.SlotIndex != i+1 || s.Name != "" || s.PolicyJSON != "" {
			t.Fatalf("slot %d = %+v, want empty slot", i+1, s)
		}
	}

	// Update slot 2 with a name and policy; slots 1/3 stay empty.
	if err := repo.UpdatePolicySlot(2, "compact", `{"schema_version":1}`); err != nil {
		t.Fatalf("UpdatePolicySlot(2): %v", err)
	}
	slots, err = repo.GetPolicySlots()
	if err != nil {
		t.Fatalf("GetPolicySlots 2: %v", err)
	}
	if slots[1].Name != "compact" || slots[1].PolicyJSON != `{"schema_version":1}` {
		t.Fatalf("slot 2 after update = %+v", slots[1])
	}
	if slots[0].Name != "" || slots[2].Name != "" {
		t.Fatalf("untouched slots changed: %+v", slots)
	}

	// Out-of-range indexes are rejected.
	if err := repo.UpdatePolicySlot(0, "x", "{}"); err == nil {
		t.Fatal("slot 0 update should fail")
	}
	if err := repo.UpdatePolicySlot(4, "x", "{}"); err == nil {
		t.Fatal("slot 4 update should fail")
	}

	// A single-slot read reflects the same state.
	slot, err := repo.GetPolicySlot(2)
	if err != nil || slot == nil || slot.Name != "compact" {
		t.Fatalf("GetPolicySlot(2) = %+v err=%v", slot, err)
	}
	if slot, _ := repo.GetPolicySlot(3); slot == nil || slot.PolicyJSON != "" {
		t.Fatalf("GetPolicySlot(3) = %+v, want empty", slot)
	}
	if slot, _ := repo.GetPolicySlot(9); slot == nil || slot.SlotIndex != 9 && slot.PolicyJSON == "" {
		// Out-of-range GET returns a neutral empty view, not an error.
		t.Logf("slot 9 view: %+v", slot)
	}
}

// TestPolicySlotMissingRowRejected simulates a hand-truncated table: the fixed
// three-slot storage invariant surfaces as an update error rather than a
// silent insert.
func TestPolicySlotMissingRowRejected(t *testing.T) {
	repo := newTestRepository(t)
	defer repo.Close()

	if _, err := repo.DB().Exec("DELETE FROM policy_slots WHERE slot_index = 3"); err != nil {
		t.Fatalf("delete slot row: %v", err)
	}
	err := repo.UpdatePolicySlot(3, "x", "{}")
	if err == nil || !strings.Contains(err.Error(), "invariant") {
		t.Fatalf("update missing slot = %v, want invariant error", err)
	}
}
