package conversion_test

import (
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/tasks/conversion"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// draftJSON builds a minimal structurally valid draft with the given
// obsolete-audio handling (empty omits the field entirely).
func draftJSON(deleteMode string) []byte {
	base := `{"schema_version":1,"classifier_tags":[],"matched":{},"unmatched":{}}`
	if deleteMode == "" {
		return []byte(base)
	}
	return []byte(strings.TrimSuffix(base, "}") + `,"delete_mode":"` + deleteMode + `"}`)
}

// TestDraftDeleteModeValidation pins the draft's obsolete-audio field: the two
// accepted values, the soft default (an absent field), and a rejection for
// anything else.
func TestDraftDeleteModeValidation(t *testing.T) {
	task := conversion.New(t.TempDir())

	for _, accepted := range []string{"", "soft", "hard"} {
		if err := task.ValidateDraft(draftJSON(accepted), nil); err != nil {
			t.Fatalf("delete_mode=%q must validate: %v", accepted, err)
		}
	}
	err := task.ValidateDraft(draftJSON("medium"), nil)
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "INVALID_DRAFT" {
		t.Fatalf("err = %v, want INVALID_DRAFT", err)
	}
}

// TestNormalizeDraftKeepsDeleteMode proves the setting survives the sparse
// round trip, so a saved choice reaches every later revision snapshot.
func TestNormalizeDraftKeepsDeleteMode(t *testing.T) {
	task := conversion.New(t.TempDir())

	canonical, _, _, err := task.NormalizeDraft(draftJSON("hard"), nil)
	if err != nil {
		t.Fatalf("NormalizeDraft: %v", err)
	}
	doc, err := conversion.ParseDraft(string(canonical))
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	if doc.DeleteMode != "hard" {
		t.Fatalf("delete mode = %q, want hard", doc.DeleteMode)
	}
}
