package conversion_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/onsei/organizer/backend/internal/adapters/settings"
	"github.com/onsei/organizer/backend/internal/conversion"
	"github.com/onsei/organizer/backend/internal/workset"
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
	task := conversion.New(nil, appconfig.NewReader(t.TempDir()))

	for _, accepted := range []string{"", "soft", "hard"} {
		if err := task.ValidateDraft(draftJSON(accepted), nil); err != nil {
			t.Fatalf("delete_mode=%q must validate: %v", accepted, err)
		}
	}
	err := task.ValidateDraft(draftJSON("medium"), nil)
	if werr, ok := workset.AsError(err); !ok || werr.Code != "INVALID_DRAFT" {
		t.Fatalf("err = %v, want INVALID_DRAFT", err)
	}
}

// TestNormalizeDraftKeepsDeleteMode proves the setting survives the sparse
// round trip, so a saved choice reaches every later revision snapshot.
func TestNormalizeDraftKeepsDeleteMode(t *testing.T) {
	task := conversion.New(nil, appconfig.NewReader(t.TempDir()))

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

// TestSeedDraftReadsTheTagLiterals pins where a new draft's classifier tags come
// from: the configuration's maintained literals, and an empty set — never an
// error and never a built-in default — when that configuration cannot be read.
func TestSeedDraftReadsTheTagLiterals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		want   []string
	}{
		{"configured", `{"prune":{"literal_tags":["SEなし"," 反転 "]}}`, []string{"SEなし", "反転"}},
		{"broken", `{not json`, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.config), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			raw, _, _ := conversion.New(nil, appconfig.NewReader(dir)).SeedDraft()
			var doc struct {
				ClassifierTags []string `json:"classifier_tags"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("seed draft is not readable: %v", err)
			}
			if len(doc.ClassifierTags) != len(tc.want) {
				t.Fatalf("classifier tags = %v, want %v", doc.ClassifierTags, tc.want)
			}
			for i := range tc.want {
				if doc.ClassifierTags[i] != tc.want[i] {
					t.Fatalf("classifier tags = %v, want %v", doc.ClassifierTags, tc.want)
				}
			}
		})
	}
}
