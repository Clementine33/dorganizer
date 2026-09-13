package workset_test

import (
	"testing"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestCreateEstablishesConversionOperation covers T01: the fixed members and
// the conversion operation are established atomically, the operation starts
// unplanned, and no unimplemented operation is addressable.
func TestCreateEstablishesConversionOperation(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")
	res, err := f.svc.CreateWorkset(f.ctx, worksetusecase.CreateRequest{
		LibraryID: "lib-1", Title: "夏季整理", FolderIDs: ids, IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	ws := res.Workset
	if !res.Created {
		t.Fatal("expected created=true")
	}
	if ws.Version != 1 {
		t.Fatalf("workset version = %d, want 1", ws.Version)
	}
	if len(ws.Members) != 2 {
		t.Fatalf("members = %+v", ws.Members)
	}
	if len(ws.Operations) != 1 || ws.Operations[0].OperationType != worksetusecase.OperationTypeConversion {
		t.Fatalf("operations = %+v", ws.Operations)
	}
	op := ws.Operations[0]
	if op.PlanningState != worksetusecase.PlanningUnplanned {
		t.Fatalf("planning_state = %q, want unplanned", op.PlanningState)
	}
	if op.Version != 1 {
		t.Fatalf("operation version = %d, want 1", op.Version)
	}

	// The seeded draft is generatable without any edit (mode + tags + outputs).
	stored := f.draft(ws.WorksetID)
	if stored.Document.Mode != "available_sources" {
		t.Fatalf("seeded mode = %q", stored.Document.Mode)
	}
	if len(stored.Document.ClassifierTags) == 0 {
		t.Fatal("seeded tags must come from config.json")
	}
	if len(stored.Document.Members) != 0 {
		t.Fatalf("seeded draft must be sparse: %+v", stored.Document.Members)
	}

	// A replay returns the same workset rather than a second one.
	replay, err := f.svc.CreateWorkset(f.ctx, worksetusecase.CreateRequest{
		LibraryID: "lib-1", Title: "夏季整理", FolderIDs: ids, IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Workset.WorksetID != ws.WorksetID {
		t.Fatalf("replay: created=%v id=%s", replay.Created, replay.Workset.WorksetID)
	}

	// Unimplemented operation types have no addressable state.
	if _, err := f.svc.GetOperation(f.ctx, ws.WorksetID, "rename"); err == nil {
		t.Fatal("unimplemented operation type must not resolve")
	} else if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "UNKNOWN_OPERATION_TYPE" {
		t.Fatalf("want UNKNOWN_OPERATION_TYPE, got %v", err)
	}
}

func TestCreateWorksetValidation(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ctx := f.ctx

	if _, err := f.svc.CreateWorkset(
		ctx,
		worksetusecase.CreateRequest{LibraryID: "lib-1", Title: "", FolderIDs: ids},
	); err == nil {
		t.Fatal("empty title should fail")
	}
	if _, err := f.svc.CreateWorkset(ctx, worksetusecase.CreateRequest{
		LibraryID: "lib-1", Title: "ok", FolderIDs: []string{ids[0], ids[0]},
	}); err == nil {
		t.Fatal("duplicate folder should fail")
	}
	if _, err := f.svc.CreateWorkset(ctx, worksetusecase.CreateRequest{
		LibraryID: "lib-1", Title: "ok", FolderIDs: []string{"nope"},
	}); err == nil {
		t.Fatal("unknown folder should fail")
	}
	if _, err := f.svc.CreateWorkset(ctx, worksetusecase.CreateRequest{
		LibraryID: "nope", Title: "ok", FolderIDs: ids,
	}); err == nil {
		t.Fatal("unknown library should fail")
	}
}

// TestRenameUsesMetadataVersionOnly covers T10: a rename advances the workset
// metadata version and leaves the operation version (and therefore any draft
// or confirmation guard) untouched.
func TestRenameUsesMetadataVersionOnly(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("旧名", ids...)
	opBefore := f.operation(ws.WorksetID)

	renamed, err := f.svc.RenameWorkset(f.ctx, ws.WorksetID, worksetusecase.RenameRequest{
		Title: "新名", IfMatchVersion: ws.Version,
	})
	if err != nil {
		t.Fatalf("RenameWorkset: %v", err)
	}
	if renamed.Title != "新名" || renamed.Version != ws.Version+1 {
		t.Fatalf("rename result: title=%q version=%d", renamed.Title, renamed.Version)
	}
	if opAfter := f.operation(ws.WorksetID); opAfter.Version != opBefore.Version {
		t.Fatalf("rename advanced the operation version: %d -> %d", opBefore.Version, opAfter.Version)
	}
	// The stale metadata version is refused.
	if _, err := f.svc.RenameWorkset(f.ctx, ws.WorksetID, worksetusecase.RenameRequest{
		Title: "再改", IfMatchVersion: ws.Version,
	}); err == nil {
		t.Fatal("stale If-Match must fail")
	}
}

// TestRenameDoesNotDirtyOperationDraft covers P02: renaming the workset never
// moves an operation from planned to needs_planning.
func TestRenameDoesNotDirtyOperationDraft(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("名字", ids...)

	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" {
		t.Fatalf("generation: %+v", gen)
	}
	if got := f.operation(ws.WorksetID).PlanningState; got != worksetusecase.PlanningPlanned {
		t.Fatalf("planning state = %q, want planned", got)
	}

	renamed, err := f.svc.RenameWorkset(f.ctx, ws.WorksetID, worksetusecase.RenameRequest{
		Title: "改过的名字", IfMatchVersion: ws.Version,
	})
	if err != nil {
		t.Fatalf("RenameWorkset: %v", err)
	}
	_ = renamed
	if got := f.operation(ws.WorksetID).PlanningState; got != worksetusecase.PlanningPlanned {
		t.Fatalf("rename dirtied the operation: %q", got)
	}
}

// TestOrphanedWorksetIsReadOnly covers T15: an orphaned workset stays readable
// but refuses every write, including generation and confirmation.
func TestOrphanedWorksetIsReadOnly(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("孤儿", ids...)
	f.exec("UPDATE worksets SET library_id = NULL WHERE id = ?", ws.WorksetID)

	view, err := f.svc.GetWorkset(f.ctx, ws.WorksetID)
	if err != nil {
		t.Fatalf("GetWorkset orphaned: %v", err)
	}
	if len(view.Operations) != 1 || view.Operations[0].PlanningState != worksetusecase.PlanningOrphaned {
		t.Fatalf("orphaned operations = %+v", view.Operations)
	}
	if _, err := f.svc.RenameWorkset(f.ctx, ws.WorksetID, worksetusecase.RenameRequest{
		Title: "x", IfMatchVersion: ws.Version,
	}); err == nil {
		t.Fatal("rename orphaned should fail")
	} else if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "ORPHANED_WORKSET" {
		t.Fatalf("expected ORPHANED_WORKSET, got %v", err)
	}
	op := f.operation(ws.WorksetID)
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: draftDoc(), IfMatchVersion: op.Version,
		},
	); err == nil {
		t.Fatal("draft save on orphaned workset should fail")
	}
	if _, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "gen-orphan",
		},
	); err == nil {
		t.Fatal("generation on orphaned workset should fail")
	}
}

// TestListWorksetsPaginationAndOrphans covers the list contract.
func TestListWorksetsPaginationAndOrphans(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	first := f.createWorkset("一", ids...)
	f.createWorkset("二", ids...)

	page, cursor, err := f.svc.ListWorksets(f.ctx, worksetusecase.ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("ListWorksets: %v", err)
	}
	if len(page) != 1 || cursor == "" {
		t.Fatalf("page = %d cursor = %q", len(page), cursor)
	}
	rest, next, err := f.svc.ListWorksets(f.ctx, worksetusecase.ListQuery{Limit: 10, Cursor: cursor})
	if err != nil {
		t.Fatalf("ListWorksets page 2: %v", err)
	}
	if len(rest) != 1 || next != "" {
		t.Fatalf("second page = %d next = %q", len(rest), next)
	}

	f.exec("UPDATE worksets SET library_id = NULL WHERE id = ?", first.WorksetID)
	active, _, err := f.svc.ListWorksets(f.ctx, worksetusecase.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListWorksets active: %v", err)
	}
	for _, v := range active {
		if v.WorksetID == first.WorksetID {
			t.Fatal("orphaned workset must be excluded by default")
		}
	}
	all, _, err := f.svc.ListWorksets(f.ctx, worksetusecase.ListQuery{Limit: 10, IncludeOrphaned: true})
	if err != nil {
		t.Fatalf("ListWorksets all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("include_orphaned list = %d, want 2", len(all))
	}
	_ = ids
}
