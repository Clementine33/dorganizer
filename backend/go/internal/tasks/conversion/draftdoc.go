package conversion

import (
	"encoding/json"
	"strings"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// DraftSchemaVersion is the operation draft document schema version.
const DraftSchemaVersion = 1

// Override unit names. They are the four things an exception can replace and
// the values carried in the response's inheritance sources.
const (
	UnitMode           = "mode"
	UnitClassifierTags = "classifier_tags"
	UnitMatched        = "matched"
	UnitUnmatched      = "unmatched"
)

// Inheritance source values: where a member's effective unit value comes from.
const (
	SourceCommon = "common"
	SourceMember = "member"
)

// DraftDoc is the persisted sparse operation draft (ADR 0004 §3). The four
// common setting groups are the base; a member overrides only the units it
// explicitly replaces. Absence means inheritance — never truthiness — so an
// explicitly empty tag list is a real override while null is not.
// DeleteMode is the obsolete-audio handling of the whole operation (soft or
// hard; empty means soft) — it is one global choice, frozen into every
// revision and never overridable per member.
type DraftDoc struct {
	SchemaVersion  int                      `json:"schema_version"`
	Mode           string                   `json:"mode,omitempty"`
	DeleteMode     string                   `json:"delete_mode,omitempty"`
	ClassifierTags []string                 `json:"classifier_tags"`
	Matched        reconcile.DesiredProfile `json:"matched"`
	Unmatched      reconcile.DesiredProfile `json:"unmatched"`
	Members        []DraftMember            `json:"members,omitempty"`
}

// DraftMember is one member's participation plus its sparse unit overrides.
// The record is omitted entirely when the member participates and inherits
// everything, so the stored document stays sparse.
type DraftMember struct {
	MemberID  string       `json:"member_id"`
	Excluded  bool         `json:"excluded,omitempty"`
	Overrides *OverrideSet `json:"overrides,omitempty"`
}

// OverrideSet holds explicitly set units. A nil field inherits the common
// value; a non-nil field — including a pointer to an empty tag slice — is an
// explicit value that later common changes never touch.
type OverrideSet struct {
	Mode           *string                   `json:"mode,omitempty"`
	ClassifierTags *[]string                 `json:"classifier_tags,omitempty"`
	Matched        *reconcile.DesiredProfile `json:"matched,omitempty"`
	Unmatched      *reconcile.DesiredProfile `json:"unmatched,omitempty"`
}

// HasUnit reports whether the override set explicitly sets a unit.
func (o *OverrideSet) HasUnit(unit string) bool {
	if o == nil {
		return false
	}
	switch unit {
	case UnitMode:
		return o.Mode != nil
	case UnitClassifierTags:
		return o.ClassifierTags != nil
	case UnitMatched:
		return o.Matched != nil
	case UnitUnmatched:
		return o.Unmatched != nil
	}
	return false
}

// Units lists the explicitly set units in the canonical unit order.
func (o *OverrideSet) Units() []string {
	out := make([]string, 0, 4)
	for _, unit := range []string{UnitMode, UnitClassifierTags, UnitMatched, UnitUnmatched} {
		if o.HasUnit(unit) {
			out = append(out, unit)
		}
	}
	return out
}

// IsEmpty reports whether the override set carries no unit at all.
func (o *OverrideSet) IsEmpty() bool { return len(o.Units()) == 0 }

// MarshalDraft serializes a normalized document to its canonical JSON form and
// its canonical content hash. Both always come from the same function so a
// stored hash and a recomputed hash agree byte for byte.
func MarshalDraft(doc *DraftDoc) (string, string, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", "", err
	}
	return string(raw), sqlite.CanonicalJSONHash(raw), nil
}

// ParseDraft strictly decodes a stored draft document.
func ParseDraft(raw string) (*DraftDoc, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc DraftDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// normalizeDraft returns the canonical form of a submitted document: member
// records in workset member order, records with neither an exclusion nor an
// override dropped (absence and "participates, inherits everything" are the
// same fact), and a nil common tag list materialized as the empty list so an
// explicitly empty tag set survives the round trip.
func normalizeDraft(doc *DraftDoc, members []*sqlite.WorksetMember) *DraftDoc {
	out := &DraftDoc{
		SchemaVersion: doc.SchemaVersion,
		Mode:          doc.Mode,
		DeleteMode:    doc.DeleteMode,
		Matched:       doc.Matched,
		Unmatched:     doc.Unmatched,
	}
	out.ClassifierTags = append([]string{}, doc.ClassifierTags...)
	if out.ClassifierTags == nil {
		out.ClassifierTags = []string{}
	}
	byID := map[string]DraftMember{}
	for _, m := range doc.Members {
		if m.Excluded || !m.Overrides.IsEmpty() {
			byID[m.MemberID] = m
		}
	}
	for _, m := range members {
		if rec, ok := byID[m.MemberID]; ok {
			out.Members = append(out.Members, rec)
		}
	}
	return out
}

// validateDraftDoc applies the structural checks shared by draft save and
// generation: JSON shape (strict decoding at the transport), unit literals and
// member identity. Business completeness — non-empty tags, declared outputs,
// positive bitrates — is deliberately not checked here: a structurally valid
// but incomplete draft is a normal, saveable editing state (ADR 0004 §3) and
// is rejected only when it must produce a plan.
func validateDraftDoc(doc *DraftDoc, members []*sqlite.WorksetMember) error {
	if doc.SchemaVersion != DraftSchemaVersion {
		return worksetusecase.NewError(
			worksetusecase.ErrKindInvalidArgument,
			"INVALID_DRAFT_SCHEMA",
			"unsupported operation draft schema version",
			nil,
		)
	}
	if err := validateMode(doc.Mode); err != nil {
		return err
	}
	if err := validateDeleteMode(doc.DeleteMode); err != nil {
		return err
	}
	if err := validateProfile("matched", doc.Matched); err != nil {
		return err
	}
	if err := validateProfile("unmatched", doc.Unmatched); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, m := range members {
		known[m.MemberID] = true
	}
	seen := map[string]bool{}
	for _, m := range doc.Members {
		if m.MemberID == "" {
			return worksetusecase.NewError(
				worksetusecase.ErrKindInvalidArgument,
				"INVALID_DRAFT_SCHEMA",
				"member records require a member_id",
				nil,
			)
		}
		if seen[m.MemberID] {
			return worksetusecase.NewError(
				worksetusecase.ErrKindInvalidArgument,
				"DUPLICATE_MEMBER",
				"duplicate member_id "+m.MemberID,
				nil,
			)
		}
		seen[m.MemberID] = true
		if !known[m.MemberID] {
			return worksetusecase.NewError(
				worksetusecase.ErrKindInvalidArgument,
				"UNKNOWN_MEMBER",
				"unknown member_id "+m.MemberID,
				nil,
			)
		}
		if err := validateOverrides(m.Overrides); err != nil {
			return err
		}
	}
	return nil
}

func validateMode(mode string) error {
	switch mode {
	case "", reconcile.ModeStrict, reconcile.ModeAvailableSources:
		return nil
	}
	return worksetusecase.NewError(
		worksetusecase.ErrKindInvalidArgument,
		"INVALID_DRAFT",
		"unsupported conversion mode "+mode,
		nil,
	)
}

// validateDeleteMode accepts the two obsolete-audio handlings; absence is the
// soft default.
func validateDeleteMode(mode string) error {
	switch mode {
	case "", string(execute.DeleteModeSoft), string(execute.DeleteModeHard):
		return nil
	}
	return worksetusecase.NewError(
		worksetusecase.ErrKindInvalidArgument,
		"INVALID_DRAFT",
		"unsupported delete mode "+mode,
		nil,
	)
}

func validateOverrides(o *OverrideSet) error {
	if o == nil {
		return nil
	}
	if o.Mode != nil {
		if err := validateMode(*o.Mode); err != nil {
			return err
		}
	}
	if o.Matched != nil {
		if err := validateProfile("matched", *o.Matched); err != nil {
			return err
		}
	}
	if o.Unmatched != nil {
		if err := validateProfile("unmatched", *o.Unmatched); err != nil {
			return err
		}
	}
	return nil
}

func validateProfile(name string, profile reconcile.DesiredProfile) error {
	if err := validateOutput(name+".lossless", profile.Lossless); err != nil {
		return err
	}
	return validateOutput(name+".encoded", profile.Encoded)
}

func validateOutput(name string, spec *reconcile.AudioOutputSpec) error {
	if spec == nil {
		return nil // an undeclared output is incomplete, not malformed
	}
	switch spec.Codec {
	case reconcile.CodecWav, reconcile.CodecFlac, reconcile.CodecMp3, reconcile.CodecAac:
	default:
		return worksetusecase.NewError(
			worksetusecase.ErrKindInvalidArgument,
			"INVALID_DRAFT",
			name+": unsupported output codec "+string(spec.Codec),
			nil,
		)
	}
	if spec.Quality != nil {
		if spec.Quality.Kind != "" && spec.Quality.Kind != reconcile.QualityBitrate {
			return worksetusecase.NewError(
				worksetusecase.ErrKindInvalidArgument,
				"INVALID_DRAFT",
				name+": unsupported quality kind "+string(spec.Quality.Kind),
				nil,
			)
		}
	}
	return nil
}
