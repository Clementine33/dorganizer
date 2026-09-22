package conversion

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
	"github.com/onsei/organizer/backend/internal/workset"
)

// PolicySlot is one of the three fixed global policy slots. PolicyJSON is empty
// while the slot is unconfigured. A slot is a reusable template only: applying
// one copies its policy into a workset draft as an inline snapshot, so editing
// a slot never changes a draft or a revision that already exists.
type PolicySlot struct {
	SlotIndex  int
	Name       string
	PolicyJSON string
	UpdatedAt  time.Time
}

// PolicySlotStore is the storage the three templates live in.
type PolicySlotStore interface {
	PolicySlots() ([]*PolicySlot, error)
	PolicySlot(slotIndex int) (*PolicySlot, error)
	UpdatePolicySlot(slotIndex int, name, policyJSON string) error
}

// slotIndexes is the fixed cardinality of the catalog: three slots, always
// present, materialized empty when unconfigured.
const (
	minPolicySlot = 1
	maxPolicySlot = 3

	// maxSlotNameRunes bounds a slot name the way the settings page shows it.
	maxSlotNameRunes = 120
)

// Slots returns the three slots in order.
func (c *Catalog) Slots() ([]*PolicySlot, error) {
	return c.slots.PolicySlots()
}

// PutSlot stores one slot under a checked name and policy, and returns what was
// stored. The write is only reached once the policy is a complete one: an
// unreadable document, a policy the rules reject and a classifier tag that does
// not resolve are all refused before anything is written, so a slot never holds
// a template that cannot be planned with.
func (c *Catalog) PutSlot(slotIndex int, name string, policy json.RawMessage) (*PolicySlot, error) {
	if slotIndex < minPolicySlot || slotIndex > maxPolicySlot {
		return nil, invalidPolicySlot()
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > maxSlotNameRunes {
		return nil, workset.NewError(
			workset.ErrKindInvalidArgument,
			"INVALID_SLOT_NAME",
			"slot name must be 1-120 characters",
			nil,
		)
	}
	if len(policy) == 0 {
		return nil, invalidPolicy(errors.New("policy is required"))
	}
	parsed, err := parseInlinePolicy(policy)
	if err != nil {
		return nil, invalidPolicy(err)
	}
	if validateErr := reconcile.ValidatePolicy(parsed); validateErr != nil {
		return nil, invalidPolicy(validateErr)
	}
	if _, resolveErr := reconcile.ResolveClassifier(parsed.ClassifierTags); resolveErr != nil {
		return nil, invalidPolicy(resolveErr)
	}
	if updateErr := c.slots.UpdatePolicySlot(slotIndex, trimmed, string(policy)); updateErr != nil {
		return nil, updateErr
	}
	slot, err := c.slots.PolicySlot(slotIndex)
	if err != nil {
		return nil, err
	}
	if slot == nil {
		return nil, workset.NewError(
			workset.ErrKindInternal,
			"INTERNAL",
			"policy slot is missing from storage",
			nil,
		)
	}
	return slot, nil
}

// invalidPolicySlot is the refusal of a slot index outside the fixed three. It
// is the same refusal the route answers a non-numeric slot with, so a caller
// cannot tell the two apart — both name a slot that does not exist.
func invalidPolicySlot() error {
	return workset.NewError(
		workset.ErrKindInvalidArgument,
		"INVALID_SLOT",
		"policy slot must be 1, 2 or 3",
		nil,
	)
}

// invalidPolicy wraps a policy refusal with its reason.
func invalidPolicy(cause error) error {
	return workset.NewError(workset.ErrKindInvalidArgument, "INVALID_POLICY", cause.Error(), cause)
}

// parseInlinePolicy decodes a raw JSON policy into the reconcile shape.
func parseInlinePolicy(raw json.RawMessage) (reconcile.Policy, error) {
	var policy reconcile.Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return policy, errors.New("inline policy is not valid JSON")
	}
	return policy, nil
}
