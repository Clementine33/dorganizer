package plan

import (
	"strings"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// resolvePolicy resolves an inline-only policy source into the resolved
// policy plus its compiled literal-tag classifier. Every classifier authority
// (slot update, draft save, generation, standalone plan create) funnels
// through reconcile.ValidatePolicy/ResolveClassifier.
func (s *serviceImpl) resolvePolicy(source PolicySource) (reconcile.Policy, *reconcile.Classifier, error) {
	if source.Kind != "inline" || source.InlinePolicy == nil {
		return reconcile.Policy{}, nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY_SOURCE", "workflow policy must be a complete inline policy", nil)
	}
	policy := *source.InlinePolicy
	if err := reconcile.ValidatePolicy(policy); err != nil {
		return policy, nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), err)
	}
	classifier, err := reconcile.ResolveClassifier(policy.ClassifierTags)
	if err != nil {
		return policy, nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), err)
	}
	return policy, &classifier, nil
}

// normalizeTagSnapshot emits the canonical persisted tag snapshot: the
// normalized set joined with NUL. Stored in plan_workflow_steps
// classifier_pattern so each revision stays self-describing.
func normalizeTagSnapshot(tags []string) string {
	return strings.Join(reconcile.NormalizeTags(tags), "\x00")
}
