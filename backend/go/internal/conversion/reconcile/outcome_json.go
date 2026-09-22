package reconcile

import "encoding/json"

// The review payload's collections are always arrays. A component with no
// operations, no variants or no files is an ordinary outcome — "无变化" — and
// clients read `.operations.length` directly, so a nil slice must never reach
// the wire as `null`. These marshalers are the single place that guarantee it,
// covering both the persisted outcome snapshot and the live create response.

// MarshalJSON emits every ComponentOutcome collection as an array.
func (c ComponentOutcome) MarshalJSON() ([]byte, error) {
	type wire ComponentOutcome
	return json.Marshal(struct {
		wire

		Lanes              []LaneDecision    `json:"lanes"`
		Variants           []VariantDecision `json:"variant_decisions"`
		Operations         []Operation       `json:"operations"`
		ProjectedInventory []string          `json:"projected_inventory"`
		Files              []FileTuple       `json:"files"`
	}{
		wire:               wire(c),
		Lanes:              orEmpty(c.Lanes),
		Variants:           orEmpty(c.Variants),
		Operations:         orEmpty(c.Operations),
		ProjectedInventory: orEmpty(c.ProjectedInventory),
		Files:              orEmpty(c.Files),
	})
}

// MarshalJSON emits a variant's decision list as an array.
func (v VariantDecision) MarshalJSON() ([]byte, error) {
	type wire VariantDecision
	return json.Marshal(struct {
		wire

		Decisions []FileDecision `json:"decisions"`
	}{
		wire:      wire(v),
		Decisions: orEmpty(v.Decisions),
	})
}

// orEmpty keeps the declared order of the shadowed fields while replacing a
// nil slice with an empty one.
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
