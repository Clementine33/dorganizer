package workset_test

import (
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// stubTask is a minimal Task for the registry tests: it seeds a fixed
// document and accepts any draft it is handed.
type stubTask struct {
	kind string
	seed string
}

func (s stubTask) Kind() string { return s.kind }

func (s stubTask) SeedDraft() ([]byte, string, int) {
	return []byte(s.seed), "hash-" + s.kind, 1
}

func (stubTask) ValidateDraft([]byte, []*sqlite.WorksetMember) error { return nil }

func (stubTask) NormalizeDraft(raw []byte, _ []*sqlite.WorksetMember) ([]byte, string, int, error) {
	return raw, "hash", 1, nil
}

// TestCreateWorksetMaterializesRegisteredTasks covers the registry contract:
// creating a workset writes one operation plus its seeded draft for every
// registered task, in registration order.
func TestCreateWorksetMaterializesRegisteredTasks(t *testing.T) {
	f := newFixture(t)
	worksetusecase.RegisterTasksForTest(f.svc, []worksetusecase.Task{
		stubTask{kind: worksetusecase.OperationTypeConversion, seed: `{"schema_version":1,"classifier_tags":[]}`},
		stubTask{kind: "rename", seed: `{"schema_version":1,"template":"{title}"}`},
	})
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("多任务", ids...)

	if len(ws.Operations) != 2 {
		t.Fatalf("operations = %+v, want one per registered task", ws.Operations)
	}
	if ws.Operations[0].OperationType != worksetusecase.OperationTypeConversion ||
		ws.Operations[1].OperationType != "rename" {
		t.Fatalf("operation order = %+v", ws.Operations)
	}
	for _, want := range []struct{ kind, seed string }{
		{worksetusecase.OperationTypeConversion, `{"schema_version":1,"classifier_tags":[]}`},
		{"rename", `{"schema_version":1,"template":"{title}"}`},
	} {
		draft, err := f.repo.GetOperationDraft(ws.WorksetID, want.kind)
		if err != nil || draft == nil {
			t.Fatalf("draft for %s: %+v err=%v", want.kind, draft, err)
		}
		if draft.DraftJSON != want.seed || draft.DraftHash != "hash-"+want.kind {
			t.Fatalf("seeded draft for %s = %+v", want.kind, draft)
		}
	}
}

// TestUnknownTaskKindHasNoAddressableState pins the registry lookup: only
// registered kinds resolve, and an unregistered one reports the stable error
// code instead of an empty operation.
func TestUnknownTaskKindHasNoAddressableState(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("未注册", ids...)

	if _, err := f.svc.GetDraft(f.ctx, ws.WorksetID, "rename"); err == nil {
		t.Fatal("an unregistered operation type must not resolve")
	} else if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "UNKNOWN_OPERATION_TYPE" {
		t.Fatalf("want UNKNOWN_OPERATION_TYPE, got %v", err)
	}
}
