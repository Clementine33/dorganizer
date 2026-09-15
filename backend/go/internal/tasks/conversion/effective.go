package conversion

import (
	"fmt"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// MemberEffective is one member's resolved conversion config plus where each
// of the four units came from. Source differences survive equal values: a
// member that explicitly repeats the common value is still "member".
type MemberEffective struct {
	MemberID   string
	FolderPath string
	Excluded   bool
	Policy     reconcile.Policy
	Sources    map[string]string // unit -> SourceCommon | SourceMember
}

// CommonPolicy is the draft's common conversion policy. Missing mode is
// interpreted as strict (the historical default); the policy schema version is
// pinned here so callers never invent one.
func CommonPolicy(doc *DraftDoc) reconcile.Policy {
	tags := doc.ClassifierTags
	if tags == nil {
		tags = []string{}
	}
	mode := doc.Mode
	if mode == "" {
		mode = reconcile.ModeStrict
	}
	return reconcile.Policy{
		SchemaVersion:  1,
		Mode:           mode,
		ClassifierTags: tags,
		Matched:        doc.Matched,
		Unmatched:      doc.Unmatched,
	}
}

// ResolveEffective resolves every member's effective config from the sparse
// draft. Excluded members are resolved too — exclusion changes participation,
// never the stored overrides — so restoring participation returns the member to
// its previous inheritance relationships (ADR 0004 §3, C11).
func ResolveEffective(doc *DraftDoc, members []*sqlite.WorksetMember) ([]MemberEffective, error) {
	if err := validateDraftDoc(doc, members); err != nil {
		return nil, err
	}
	common := CommonPolicy(doc)
	byID := map[string]DraftMember{}
	for _, m := range doc.Members {
		byID[m.MemberID] = m
	}
	out := make([]MemberEffective, 0, len(members))
	for _, m := range members {
		rec, ok := byID[m.MemberID]
		eff := MemberEffective{
			MemberID:   m.MemberID,
			FolderPath: m.FolderPath,
			Policy:     common,
			Sources: map[string]string{
				UnitMode:           SourceCommon,
				UnitClassifierTags: SourceCommon,
				UnitMatched:        SourceCommon,
				UnitUnmatched:      SourceCommon,
			},
		}
		if ok {
			eff.Excluded = rec.Excluded
			applyOverrides(&eff, rec.Overrides)
		}
		out = append(out, eff)
	}
	return out, nil
}

// applyOverrides replaces only the units the member explicitly set.
func applyOverrides(eff *MemberEffective, o *OverrideSet) {
	if o == nil {
		return
	}
	if o.Mode != nil {
		eff.Policy.Mode = *o.Mode
		if eff.Policy.Mode == "" {
			eff.Policy.Mode = reconcile.ModeStrict
		}
		eff.Sources[UnitMode] = SourceMember
	}
	if o.ClassifierTags != nil {
		eff.Policy.ClassifierTags = *o.ClassifierTags
		eff.Sources[UnitClassifierTags] = SourceMember
	}
	if o.Matched != nil {
		eff.Policy.Matched = *o.Matched
		eff.Sources[UnitMatched] = SourceMember
	}
	if o.Unmatched != nil {
		eff.Policy.Unmatched = *o.Unmatched
		eff.Sources[UnitUnmatched] = SourceMember
	}
}

// ParticipatingPolicyMap is the runner's per-root policy map: the effective
// config of every participating member, keyed by planning root. Excluded
// members are absent — they stay in the frozen snapshot but never plan.
func ParticipatingPolicyMap(effective []MemberEffective) map[string]reconcile.Policy {
	out := make(map[string]reconcile.Policy, len(effective))
	for _, e := range effective {
		if e.Excluded {
			continue
		}
		out[e.FolderPath] = e.Policy
	}
	return out
}

// ExcludedMemberIDs lists the excluded member ids in member order.
func ExcludedMemberIDs(effective []MemberEffective) []string {
	out := make([]string, 0)
	for _, e := range effective {
		if e.Excluded {
			out = append(out, e.MemberID)
		}
	}
	return out
}

// ResolveExecutable resolves every member's effective config and validates
// that the draft can actually produce a plan: structural validity plus full
// business validation of every participating member's effective config. This
// is the generation boundary check; draft save deliberately does not apply it.
func ResolveExecutable(
	doc *DraftDoc,
	members []*sqlite.WorksetMember,
) ([]MemberEffective, error) {
	effective, err := ResolveEffective(doc, members)
	if err != nil {
		return nil, err
	}
	participating := 0
	for _, e := range effective {
		if e.Excluded {
			continue
		}
		participating++
		if err := reconcile.ValidatePolicy(e.Policy); err != nil {
			return nil, worksetusecase.NewError(
				worksetusecase.ErrKindInvalidArgument,
				"INVALID_POLICY",
				fmt.Sprintf("effective settings for member %s: %s", e.MemberID, err.Error()),
				nil,
			)
		}
	}
	if participating == 0 {
		return nil, worksetusecase.NewError(
			worksetusecase.ErrKindConflict,
			"NO_ACTIVE_MEMBERS",
			"every member is excluded; restore at least one member to generate",
			nil,
		)
	}
	return effective, nil
}
