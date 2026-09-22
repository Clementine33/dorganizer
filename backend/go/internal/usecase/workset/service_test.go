package workset_test

import (
	"strconv"
	"testing"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestCreateEstablishesConversionOperation covers creating the current
// record materializes the requested operation only, and the operation starts
// unplanned and addressable.
func TestCreateEstablishesConversionOperation(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")
	res, err := f.svc.CreateCurrentWorkset(f.ctx, worksetusecase.CreateCurrentRequest{
		LibraryID: "lib-1", Title: "夏季整理", OperationType: worksetusecase.OperationTypeConversion,
		FolderPaths: ids, IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateCurrentWorkset: %v", err)
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
	if mustDraft(t, stored).Mode != "available_sources" {
		t.Fatalf("seeded mode = %q", mustDraft(t, stored).Mode)
	}
	if len(mustDraft(t, stored).ClassifierTags) == 0 {
		t.Fatal("seeded tags must come from config.json")
	}
	if len(mustDraft(t, stored).Members) != 0 {
		t.Fatalf("seeded draft must be sparse: %+v", mustDraft(t, stored).Members)
	}

	// A replay returns the same record rather than a second one.
	replay, err := f.svc.CreateCurrentWorkset(f.ctx, worksetusecase.CreateCurrentRequest{
		LibraryID: "lib-1", Title: "夏季整理", OperationType: worksetusecase.OperationTypeConversion,
		FolderPaths: ids, IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Workset.WorksetID != ws.WorksetID {
		t.Fatalf("replay: created=%v id=%s", replay.Created, replay.Workset.WorksetID)
	}
	if res.Recorded != 2 || len(res.Skipped) != 0 {
		t.Fatalf("recorded=%d skipped=%+v", res.Recorded, res.Skipped)
	}

	// Unimplemented operation types have no addressable state.
	if _, err := f.svc.GetOperation(f.ctx, ws.WorksetID, "rename"); err == nil {
		t.Fatal("unimplemented operation type must not resolve")
	} else if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "UNKNOWN_OPERATION_TYPE" {
		t.Fatalf("want UNKNOWN_OPERATION_TYPE, got %v", err)
	}
}

// TestCreateCurrentSkipsUnusableFolders covers the scope reporting: a
// directory that cannot join is left out with its reason instead of being
// silently dropped or silently kept, and a selection with nothing usable
// creates no record at all.
func TestCreateCurrentSkipsUnusableFolders(t *testing.T) {
	f := newFixture(t)
	f.standardLibrary("albumA")
	f.insertDir("/music", "emptyDir", false)
	f.insertDir("/music", "nested/child", true)

	res, err := f.svc.CreateCurrentWorkset(f.ctx, worksetusecase.CreateCurrentRequest{
		LibraryID:      "lib-1",
		OperationType:  worksetusecase.OperationTypeConversion,
		FolderPaths:    []string{"albumA", "../escape", "emptyDir", "albumA", "nope", "nested/child"},
		IdempotencyKey: "idem-skip",
	})
	if err != nil {
		t.Fatalf("CreateCurrentWorkset: %v", err)
	}
	if res.Recorded != 1 || res.Created != true {
		t.Fatalf("recorded=%d created=%v", res.Recorded, res.Created)
	}
	if len(res.Workset.Members) != 1 || res.Workset.Members[0].RelPath != "albumA" {
		t.Fatalf("members = %+v", res.Workset.Members)
	}
	reasons := map[string]string{}
	for _, skipped := range res.Skipped {
		reasons[skipped.Path] = skipped.Reason
	}
	want := map[string]string{
		"../escape":    worksetusecase.SkipInvalidPath,
		"emptyDir":     worksetusecase.SkipNoAudio,
		"albumA":       worksetusecase.SkipDuplicate,
		"nope":         worksetusecase.SkipMissing,
		"nested/child": worksetusecase.SkipNotDirect,
	}
	for path, reason := range want {
		if reasons[path] != reason {
			t.Errorf("skipped[%q] = %q, want %q (all: %+v)", path, reasons[path], reason, res.Skipped)
		}
	}

	// Nothing usable: no record is created, and the refusal names why.
	_, err = f.svc.CreateCurrentWorkset(f.ctx, worksetusecase.CreateCurrentRequest{
		LibraryID:      "lib-1",
		OperationType:  worksetusecase.OperationTypeConversion,
		FolderPaths:    []string{"emptyDir"},
		IdempotencyKey: "idem-none",
	})
	if err == nil {
		t.Fatal("a selection with no usable directory must not create a record")
	}
	werr, ok := worksetusecase.AsError(err)
	if !ok || werr.Code != "NO_AUDIO_MEMBERS" {
		t.Fatalf("want NO_AUDIO_MEMBERS, got %v", err)
	}
	if len(werr.Details) == 0 {
		t.Fatal("the refusal must name the folders it could not use")
	}
	// The refusal left the record that was already there untouched.
	after, _ := f.svc.GetCurrentWorkset(f.ctx, "lib-1", worksetusecase.OperationTypeConversion)
	if after == nil || after.WorksetID != res.Workset.WorksetID || len(after.Members) != 1 {
		t.Fatalf("a refused creation changed the current record: %+v", after)
	}
}

// TestCreateCurrentValidation covers the request-level refusals.
func TestCreateCurrentValidation(t *testing.T) {
	f := newFixture(t)
	f.standardLibrary("albumA")
	ctx := f.ctx
	base := func() worksetusecase.CreateCurrentRequest {
		return worksetusecase.CreateCurrentRequest{
			LibraryID:      "lib-1",
			OperationType:  worksetusecase.OperationTypeConversion,
			FolderPaths:    []string{"albumA"},
			IdempotencyKey: "idem-validate",
		}
	}

	noFolders := base()
	noFolders.FolderPaths = nil
	if _, err := f.svc.CreateCurrentWorkset(ctx, noFolders); err == nil {
		t.Fatal("an empty selection should fail")
	}
	tooMany := base()
	tooMany.FolderPaths = make([]string, worksetusecase.MaxMembers+1)
	for i := range tooMany.FolderPaths {
		tooMany.FolderPaths[i] = "f" + strconv.Itoa(i)
	}
	if _, err := f.svc.CreateCurrentWorkset(ctx, tooMany); err == nil {
		t.Fatal("more than the member cap should fail, not truncate")
	}
	unknownOp := base()
	unknownOp.OperationType = "rename"
	if _, err := f.svc.CreateCurrentWorkset(ctx, unknownOp); err == nil {
		t.Fatal("an unregistered operation type should fail")
	}
	unknownLib := base()
	unknownLib.LibraryID = "nope"
	if _, err := f.svc.CreateCurrentWorkset(ctx, unknownLib); err == nil {
		t.Fatal("an unknown library should fail")
	}
}

// TestCreateCurrentReplacesTheRecordTheCallerSaw covers the replacement rule:
// the old record, its plan and its sessions go in the same transaction that
// publishes the new one.
func TestCreateCurrentReplacesTheRecordTheCallerSaw(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	first := f.createCurrent("第一版", ids...)
	generated := f.runGeneration(first.WorksetID, f.operation(first.WorksetID).Version)
	if generated.Status != "completed" {
		t.Fatalf("generation: %+v", generated)
	}
	planID := f.operation(first.WorksetID).CurrentRevision.PlanID

	second := f.createCurrent("第二版", "albumB")
	if second.WorksetID == first.WorksetID {
		t.Fatal("the replacement must be a new record")
	}
	current, err := f.svc.GetCurrentWorkset(f.ctx, "lib-1", worksetusecase.OperationTypeConversion)
	if err != nil || current == nil || current.WorksetID != second.WorksetID {
		t.Fatalf("current record = %+v err=%v", current, err)
	}
	if _, err := f.svc.GetWorkset(f.ctx, first.WorksetID); err == nil {
		t.Fatal("the replaced record must be gone")
	}
	if _, err := f.svc.GetRevision(f.ctx, first.WorksetID, worksetusecase.OperationTypeConversion, planID); err == nil {
		t.Fatal("the replaced record's plan must be gone")
	}
	var leftovers int
	f.queryInt(&leftovers, "SELECT COUNT(*) FROM plans WHERE workset_id = ?", first.WorksetID)
	if leftovers != 0 {
		t.Fatalf("replaced plans left behind: %d", leftovers)
	}
}

// TestCreateCurrentRefusesAStaleExpectation covers the concurrency rule: a
// caller that saw another record as current must not overwrite the newer one.
func TestCreateCurrentRefusesAStaleExpectation(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")
	first := f.createCurrent("第一版", ids...)

	// A second client still believes there is no record.
	f.ensurePlaceholders()
	_, err := f.svc.CreateCurrentWorkset(f.ctx, worksetusecase.CreateCurrentRequest{
		LibraryID:      "lib-1",
		OperationType:  worksetusecase.OperationTypeConversion,
		FolderPaths:    []string{"albumB"},
		IdempotencyKey: "idem-stale",
	})
	if err == nil {
		t.Fatal("a stale expectation must be refused")
	}
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "RECORD_REPLACED" {
		t.Fatalf("want RECORD_REPLACED, got %v", err)
	}
	current, _ := f.svc.GetCurrentWorkset(f.ctx, "lib-1", worksetusecase.OperationTypeConversion)
	if current == nil || current.WorksetID != first.WorksetID {
		t.Fatalf("the newer record was overwritten: %+v", current)
	}

	// The same client retries with the id it now sees.
	replaced := f.createCurrent("第二版", "albumB")
	if replaced.WorksetID == first.WorksetID {
		t.Fatal("the retry must create the replacement")
	}
}

// TestCreateCurrentRefusesABusyRecord covers a record with a queued or
// running session is never replaced, and it keeps its plan and members.
func TestCreateCurrentRefusesABusyRecord(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")
	first := f.createCurrent("占用", ids...)
	// A queued session that nobody runs: the fixture's dispatcher is not
	// started, so the session stays queued for the whole test.
	f.startGeneration(first.WorksetID, f.operation(first.WorksetID).Version)

	f.ensurePlaceholders()
	_, err := f.svc.CreateCurrentWorkset(f.ctx, worksetusecase.CreateCurrentRequest{
		LibraryID:         "lib-1",
		OperationType:     worksetusecase.OperationTypeConversion,
		FolderPaths:       []string{"albumB"},
		ExpectedCurrentID: first.WorksetID,
		IdempotencyKey:    "idem-busy",
	})
	if err == nil {
		t.Fatal("replacing a busy record must be refused")
	}
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "RECORD_BUSY" {
		t.Fatalf("want RECORD_BUSY, got %v", err)
	}
	current, _ := f.svc.GetCurrentWorkset(f.ctx, "lib-1", worksetusecase.OperationTypeConversion)
	if current == nil || current.WorksetID != first.WorksetID || len(current.Members) != 2 {
		t.Fatalf("the busy record changed: %+v", current)
	}
}

// TestCreateCurrentRejectsAReusedKeyWithAnotherRequest covers the key rule: an
// idempotency key belongs to the request that used it.
func TestCreateCurrentRejectsAReusedKeyWithAnotherRequest(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")
	f.createCurrent("キー", ids...)

	f.ensurePlaceholders()
	_, err := f.svc.CreateCurrentWorkset(f.ctx, worksetusecase.CreateCurrentRequest{
		LibraryID:      "lib-1",
		OperationType:  worksetusecase.OperationTypeConversion,
		FolderPaths:    []string{"albumB"},
		IdempotencyKey: "create-キー",
	})
	if err == nil {
		t.Fatal("the same key with a different request must conflict")
	}
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("want IDEMPOTENCY_KEY_REUSED, got %v", err)
	}
}

// TestRecordScopeIsPerLibraryAndOperation covers the ownership rule: one record per
// (library, operation), and another library keeps its own.
func TestRecordScopeIsPerLibraryAndOperation(t *testing.T) {
	f := newFixture(t)
	f.standardLibrary("albumA")
	f.insertLibrary("lib-2", "/music2")
	f.insertDir("/music2", "albumC", true)

	first := f.createCurrent("一库", "albumA")
	second := f.createCurrentFor("lib-2", "二库", "albumC")
	if first.WorksetID == second.WorksetID {
		t.Fatal("records of different libraries must be different records")
	}
	one, _ := f.svc.GetCurrentWorkset(f.ctx, "lib-1", worksetusecase.OperationTypeConversion)
	two, _ := f.svc.GetCurrentWorkset(f.ctx, "lib-2", worksetusecase.OperationTypeConversion)
	if one == nil || one.WorksetID != first.WorksetID || two == nil || two.WorksetID != second.WorksetID {
		t.Fatalf("current records: %+v / %+v", one, two)
	}
	// A library with no record answers "none", which is not an error.
	none, err := f.svc.GetCurrentWorkset(f.ctx, "lib-1", "rename")
	if err == nil {
		t.Fatalf("an unregistered operation type must not resolve: %+v", none)
	}
}

// TestRenameUsesMetadataVersionOnly covers a rename advances the workset
// metadata version and leaves the operation version (and therefore any draft
// generation or execution guard) untouched.
func TestRenameUsesMetadataVersionOnly(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createCurrent("旧名", ids...)
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

// TestRenameDoesNotDirtyOperationDraft covers renaming the workset never
// moves an operation from planned to needs_planning.
func TestRenameDoesNotDirtyOperationDraft(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createCurrent("名字", ids...)

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

// TestOrphanedWorksetIsReadOnly covers an orphaned workset stays readable
// but refuses every write, including generation, draft edits and execution.
func TestOrphanedWorksetIsReadOnly(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createCurrent("孤儿", ids...)
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
			Document: draftJSON(t, draftDoc()), IfMatchVersion: op.Version,
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
	f.insertLibrary("lib-2", "/music2")
	f.insertDir("/music2", "albumC", true)
	first := f.createCurrent("一", ids...)
	f.createCurrentFor("lib-2", "二", "albumC")

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
