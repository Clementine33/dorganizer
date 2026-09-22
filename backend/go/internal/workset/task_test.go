package workset_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/workset"
)

// stubTask is a minimal second task (rename-shaped) for the registry and seam
// tests: it seeds a fixed document of its own shape, plans one unit per root
// and runs them without touching disk. It proves the generic paths never need
// to know conversion.
type stubTask struct {
	kind  string
	seed  string
	moved bool
}

func (s stubTask) Kind() string { return s.kind }

func (s stubTask) SeedDraft() ([]byte, string, int) {
	return []byte(s.seed), "hash-" + s.kind, 1
}

func (stubTask) ValidateDraft(raw []byte, _ []*workset.WorksetMember) error {
	if len(raw) == 0 {
		return workset.NewError(workset.ErrKindInvalidArgument, "INVALID_DRAFT", "empty draft", nil)
	}
	return nil
}

func (stubTask) NormalizeDraft(raw []byte, _ []*workset.WorksetMember) ([]byte, string, int, error) {
	if len(raw) == 0 {
		return nil, "", 0, workset.NewError(
			workset.ErrKindInvalidArgument,
			"INVALID_DRAFT",
			"empty draft",
			nil,
		)
	}
	return raw, "hash-normalized", 1, nil
}

func (stubTask) ValidateSessionInput([]byte, []*workset.WorksetMember) error { return nil }

func (s stubTask) PlanSession(
	_ context.Context,
	in workset.PlanSessionInput,
) (*workset.PlanSnapshot, error) {
	snap := &workset.PlanSnapshot{
		RootPath:             "stub-scope",
		Payload:              json.RawMessage(`{"schema_version":1}`),
		PayloadHash:          "payload-hash",
		PayloadSchemaVersion: 1,
		Summary:              json.RawMessage(`{"summary_reason":"NO_MATCH"}`),
		Status:               "ok",
	}
	for i, m := range in.Members {
		snap.Roots = append(snap.Roots, workset.PlanRootFacts{
			Index: i, Path: m.FolderPath, Identity: m.FolderPath, Status: "ok", InventoryFingerprint: "fp",
		})
		snap.Units = append(snap.Units, workset.PlannedUnit{
			Index: i, RootIndex: i, ID: "unit-" + m.MemberID, Status: "ok", Payload: json.RawMessage(`{}`),
		})
	}
	if in.Progress != nil {
		for i := range in.Members {
			in.Progress(workset.PlanProgress{
				CompletedRoots: i + 1, TotalRoots: len(in.Members), CurrentRoot: in.Members[i].FolderPath,
			})
		}
	}
	return snap, nil
}

func (s stubTask) EvaluateRevision(
	_ workset.RevisionFacts,
) (workset.RevisionHealth, error) {
	return workset.RevisionHealth{InputMoved: s.moved}, nil
}

func (stubTask) FreezeExecution(
	in workset.RevisionFacts,
) (workset.FrozenExecution, []string, error) {
	frozen := workset.FrozenExecution{Options: json.RawMessage(`{}`)}
	if in.Detail != nil {
		for _, c := range in.Detail.Components {
			frozen.Units = append(frozen.Units, workset.ExecutionUnit{
				Index: c.ComponentIndex, RootIndex: c.RootIndex, ID: c.ComponentID, Payload: json.RawMessage(`{}`),
			})
		}
	}
	return frozen, nil, nil
}

func (stubTask) PrepareUnit(
	context.Context, workset.UnitRunInput,
) (workset.PreparedUnit, error) {
	return stubUnit{}, nil
}

// stubUnit is the prepared form of a stub unit: nothing to encode and nothing
// to report.
type stubUnit struct{}

func (stubUnit) EncodeTasks() int { return 0 }

func (stubUnit) EncodeTask(context.Context, int) error { return nil }

func (stubUnit) Commit(context.Context) (workset.UnitResult, error) {
	return stubFacts(), nil
}

func (stubUnit) Discard(error) workset.UnitResult { return stubFacts() }

// stubFacts is an empty fact set with no nil lists, the shape the report
// persists.
func stubFacts() workset.UnitResult {
	return workset.UnitResult{
		Committed: []string{}, Removed: []string{}, Remaining: []string{}, Recovery: []string{},
	}
}

func (stubTask) RevisionMembers(in workset.RevisionFacts) ([]workset.RevisionMemberFacts, error) {
	out := make([]workset.RevisionMemberFacts, 0, len(in.Members))
	for _, m := range in.Members {
		out = append(out, workset.RevisionMemberFacts{
			MemberID: m.MemberID, FolderPath: m.FolderPath, Payload: json.RawMessage(`{}`),
		})
	}
	return out, nil
}

func (stubTask) ReviewRevision(in workset.RevisionFacts) (workset.PlanReview, error) {
	out := workset.PlanReview{Payload: json.RawMessage(`{"schema_version":1}`)}
	if in.Detail != nil {
		for _, c := range in.Detail.Components {
			out.Units = append(out.Units, workset.UnitReview{Payload: json.RawMessage(c.OutcomeJSON)})
		}
	}
	return out, nil
}

// TestCreateCurrentMaterializesOnlyTheRequestedTask covers the registry
// contract: creating a record writes the operation that was requested and its
// seeded draft, and never a task nobody asked for (ADR 0001 §1).
func TestCreateCurrentMaterializesOnlyTheRequestedTask(t *testing.T) {
	f := newFixture(t)
	workset.RegisterTasksForTest(f.svc, []workset.Task{
		stubTask{kind: workset.OperationTypeConversion, seed: `{"schema_version":1,"classifier_tags":[]}`},
		stubTask{kind: "rename", seed: `{"schema_version":1,"template":"{title}"}`},
	})
	ids := f.standardLibrary("albumA")
	ws := f.createCurrentOperation("lib-1", "rename", ids...)

	if len(ws.Operations) != 1 || ws.Operations[0].OperationType != "rename" {
		t.Fatalf("operations = %+v, want only the requested one", ws.Operations)
	}
	draft, err := f.repo.GetOperationDraft(ws.WorksetID, "rename")
	if err != nil || draft == nil {
		t.Fatalf("draft for rename: %+v err=%v", draft, err)
	}
	if draft.DraftJSON != `{"schema_version":1,"template":"{title}"}` || draft.DraftHash != "hash-rename" {
		t.Fatalf("seeded draft = %+v", draft)
	}
	if _, convErr := f.repo.GetOperation(ws.WorksetID, workset.OperationTypeConversion); convErr == nil {
		t.Fatal("an operation nobody requested must not be materialized")
	}
	// The conversion record of the same library is a different record.
	conv := f.createCurrent("转换", ids...)
	if conv.WorksetID == ws.WorksetID {
		t.Fatal("operations are separate records")
	}
}

// TestUnknownTaskKindHasNoAddressableState pins the registry lookup: only
// registered kinds resolve, and an unregistered one reports the stable error
// code instead of an empty operation.
func TestUnknownTaskKindHasNoAddressableState(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createCurrent("未注册", ids...)

	if _, err := f.svc.GetDraft(f.ctx, ws.WorksetID, "rename"); err == nil {
		t.Fatal("an unregistered operation type must not resolve")
	} else if werr, ok := workset.AsError(err); !ok || werr.Code != "UNKNOWN_OPERATION_TYPE" {
		t.Fatalf("want UNKNOWN_OPERATION_TYPE, got %v", err)
	}
}

// operationFor reads one operation's view by type.
func (f *fixture) operationFor(worksetID, operationType string) *workset.OperationView {
	f.t.Helper()
	view, err := f.svc.GetOperation(f.ctx, worksetID, operationType)
	if err != nil {
		f.t.Fatalf("GetOperation(%s): %v", operationType, err)
	}
	return view
}

// runGenerationFor drives the real dispatcher until the session of one
// operation reaches a terminal status.
func (f *fixture) runGenerationFor(worksetID, operationType, key string, ifMatch int) *workset.GenerationView {
	f.t.Helper()
	res, err := f.svc.StartGeneration(f.ctx, worksetID, operationType, workset.StartGenerationRequest{
		IfMatchVersion: ifMatch, IdempotencyKey: key,
	})
	if err != nil {
		f.t.Fatalf("StartGeneration(%s): %v", operationType, err)
	}
	f.startOnce.Do(func() {
		f.svc.DispatcherHandle().Start()
		f.t.Cleanup(f.svc.DispatcherHandle().Stop)
	})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		g, err := f.svc.GetGeneration(f.ctx, worksetID, operationType, res.Generation.GenerationID)
		if err != nil {
			f.t.Fatalf("GetGeneration: %v", err)
		}
		switch g.Status {
		case workset.GenStatusCompleted,
			workset.GenStatusFailed,
			workset.GenStatusCanceled,
			workset.GenStatusInterrupted:
			return g
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("generation did not finish")
	return nil
}

// awaitExecution polls one execution session until it reaches a terminal status.
func (f *fixture) awaitExecution(worksetID, operationType, executionID string) *workset.ExecutionView {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		v, err := f.svc.GetExecution(f.ctx, worksetID, operationType, executionID, workset.ExecutionPage{})
		if err != nil {
			f.t.Fatalf("GetExecution: %v", err)
		}
		switch v.Status {
		case "succeeded", "failed", "canceled", "interrupted":
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("execution did not finish")
	return nil
}

// saveRenameDraft writes one draft document of the second task's own shape.
func (f *fixture) saveRenameDraft(worksetID string) {
	f.t.Helper()
	op := f.operationFor(worksetID, "rename")
	if _, err := f.svc.SaveDraft(f.ctx, worksetID, "rename", workset.SaveDraftRequest{
		Document:       json.RawMessage(`{"schema_version":1,"template":"{title} - {artist}"}`),
		IfMatchVersion: op.Version,
	}); err != nil {
		f.t.Fatalf("SaveDraft(rename): %v", err)
	}
}

// TestSecondTaskRunsTheFullChain drives the generic paths with a second task
// that shares nothing with conversion: draft → generation → execution, all
// through the workset seam.
func TestSecondTaskRunsTheFullChain(t *testing.T) {
	f := newFixture(t)
	workset.RegisterTasksForTest(f.svc, []workset.Task{
		stubTask{kind: workset.OperationTypeConversion, seed: `{"schema_version":1}`},
		stubTask{kind: "rename", seed: `{"schema_version":1,"template":"{title}"}`},
	})
	ids := f.standardLibrary("albumA")
	ws := f.createCurrentOperation("lib-1", "rename", ids...)

	f.saveRenameDraft(ws.WorksetID)
	op := f.operationFor(ws.WorksetID, "rename")
	gen := f.runGenerationFor(ws.WorksetID, "rename", "k-chain", op.Version)
	if gen.Status != workset.GenStatusCompleted || gen.RevisionID == "" {
		t.Fatalf("generation = %+v", gen)
	}

	op = f.operationFor(ws.WorksetID, "rename")
	res, err := f.svc.StartExecution(
		f.ctx,
		ws.WorksetID,
		"rename",
		gen.RevisionID,
		workset.StartExecutionRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "k-chain",
		},
	)
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	view := f.awaitExecution(ws.WorksetID, "rename", res.Execution.ExecutionID)
	if view.Status != "succeeded" || len(view.Components) != 1 || view.Components[0].Status == "" {
		t.Fatalf("execution = %+v", view)
	}

	// A terminal session never regresses: canceling it keeps its status.
	canceled, err := f.svc.CancelExecution(f.ctx, ws.WorksetID, "rename", res.Execution.ExecutionID)
	if err != nil || canceled.Status != "succeeded" {
		t.Fatalf("cancel of a terminal session = %+v err=%v", canceled, err)
	}
}

// TestSecondTaskMovedInputRefusesExecute covers the admission contract
// through the generic gates: the task reports the business reason, the
// generic side names it.
func TestSecondTaskMovedInputRefusesExecute(t *testing.T) {
	f := newFixture(t)
	workset.RegisterTasksForTest(f.svc, []workset.Task{
		stubTask{kind: "rename", seed: `{"schema_version":1,"template":"{title}"}`},
	})
	ids := f.standardLibrary("albumA")
	ws := f.createCurrentOperation("lib-1", "rename", ids...)

	f.saveRenameDraft(ws.WorksetID)
	op := f.operationFor(ws.WorksetID, "rename")
	gen := f.runGenerationFor(ws.WorksetID, "rename", "k-refuse", op.Version)
	if gen.Status != workset.GenStatusCompleted {
		t.Fatalf("generation = %+v", gen)
	}

	// The input facts move after the plan was frozen: the revision cannot run.
	workset.RegisterTasksForTest(f.svc, []workset.Task{
		stubTask{kind: "rename", seed: `{"schema_version":1,"template":"{title}"}`, moved: true},
	})
	op = f.operationFor(ws.WorksetID, "rename")
	_, err := f.svc.StartExecution(f.ctx, ws.WorksetID, "rename", gen.RevisionID, workset.StartExecutionRequest{
		IfMatchVersion: op.Version, IdempotencyKey: "k-refuse-exec",
	})
	if err == nil {
		t.Fatal("moved input must refuse execution")
	}
	if werr, ok := workset.AsError(err); !ok || werr.Code != "PLAN_NOT_EXECUTABLE" ||
		len(werr.Details) != 1 || werr.Details[0] != "INPUT_CHANGED" {
		t.Fatalf("want PLAN_NOT_EXECUTABLE INPUT_CHANGED, got %v", err)
	}
}
