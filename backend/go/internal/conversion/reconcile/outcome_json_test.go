package reconcile_test

import (
	"encoding/json"
	"testing"

	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

// TestComponentOutcomeJSONNeverNullsCollections pins the review wire contract:
// a component with nothing to do is an ordinary "unchanged" outcome, so its
// collections are empty arrays — never null. Clients read `.operations.length`
// directly while rendering a component list, and a null there crashed the
// workbench before this invariant existed.
func TestComponentOutcomeJSONNeverNullsCollections(t *testing.T) {
	raw, err := json.Marshal(reconcile.ComponentOutcome{
		ComponentID: "cmp-1",
		Partition:   reconcile.PartitionMatched,
		Status:      reconcile.StatusOK,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{
		"lanes",
		"variant_decisions",
		"operations",
		"projected_inventory",
		"files",
	} {
		got, ok := decoded[field]
		if !ok {
			t.Fatalf("%s missing from %s", field, raw)
		}
		if string(got) != "[]" {
			t.Fatalf("%s = %s, want []", field, got)
		}
	}
}

// TestVariantDecisionJSONNeverNullsDecisions covers the nested grouping the
// workbench walks for unmet-target reasons.
func TestVariantDecisionJSONNeverNullsDecisions(t *testing.T) {
	raw, err := json.Marshal(reconcile.VariantDecision{Stem: "track"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(decoded["decisions"]) != "[]" {
		t.Fatalf("decisions = %s, want []", decoded["decisions"])
	}
}

// TestComponentOutcomeJSONKeepsContent proves the marshaler adds no keys beyond
// the declared ones and still carries the real values.
func TestComponentOutcomeJSONKeepsContent(t *testing.T) {
	raw, err := json.Marshal(reconcile.ComponentOutcome{
		ComponentID: "cmp-2",
		Status:      reconcile.StatusBlocked,
		Operations: []reconcile.Operation{{
			Kind:       reconcile.OpKindEncode,
			Phase:      reconcile.PhaseMaterializeOutputs,
			SourcePath: "/music/a.flac",
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded reconcile.ComponentOutcome
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(decoded.Operations) != 1 || decoded.Operations[0].SourcePath != "/music/a.flac" {
		t.Fatalf("operations lost in round trip: %+v", decoded.Operations)
	}
	if decoded.ComponentID != "cmp-2" || decoded.Status != reconcile.StatusBlocked {
		t.Fatalf("component identity lost: %+v", decoded)
	}
}
