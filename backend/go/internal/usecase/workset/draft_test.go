package workset_test

import (
	"testing"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestDraftSaveAdvancesOperationVersion covers the single-version contract:
// the draft has no separate concurrency counter, the save returns the new
// operation version, and a stale If-Match is refused.
func TestDraftSaveAdvancesOperationVersion(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("版本", ids...)

	saved := f.saveDraft(ws.WorksetID, draftDoc(), ws.Operations[0].Version)
	if saved.Version != ws.Operations[0].Version+1 {
		t.Fatalf("operation version = %d, want %d", saved.Version, ws.Operations[0].Version+1)
	}
	if saved.PlanningState != worksetusecase.PlanningUnplanned {
		t.Fatalf("planning state = %q", saved.PlanningState)
	}
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: draftDoc(), IfMatchVersion: ws.Operations[0].Version,
		},
	); err == nil {
		t.Fatal("stale If-Match must be refused")
	} else if werr, ok := worksetusecase.AsError(
		err,
	); !ok ||
		werr.Code != "VERSION_CONFLICT" {
		t.Fatalf("want VERSION_CONFLICT, got %v", err)
	}
	// A rename must not invalidate the operation version: the same If-Match
	// works after the workset metadata version moved.
	if _, err := f.svc.RenameWorkset(f.ctx, ws.WorksetID, worksetusecase.RenameRequest{
		Title: "改名", IfMatchVersion: ws.Version,
	}); err != nil {
		t.Fatalf("RenameWorkset: %v", err)
	}
	f.saveDraft(ws.WorksetID, draftDoc(), saved.Version)
}

// TestDraftSaveAllowsIncompleteButGenerationRejects covers the C13 boundary:
// an incomplete draft is a legal editing state and only generation refuses it.
func TestDraftSaveAllowsIncompleteButGenerationRejects(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("不完整", ids...)

	doc := draftDoc()
	doc.ClassifierTags = []string{} // no classifier tag: saveable, not plannable
	view := f.saveDraft(ws.WorksetID, doc, ws.Operations[0].Version)

	_, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: view.Version, IdempotencyKey: "gen-incomplete",
		},
	)
	if err == nil {
		t.Fatal("generation with an incomplete draft must fail")
	}
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "INVALID_POLICY" {
		t.Fatalf("want INVALID_POLICY, got %v", err)
	}
}

// TestDraftRejectsUnknownAndDuplicateMembers covers the server-side member
// identity checks.
func TestDraftRejectsUnknownAndDuplicateMembers(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("成员校验", ids...)
	version := ws.Operations[0].Version

	unknown := draftDoc()
	unknown.Members = []worksetusecase.DraftMember{{MemberID: "m-not-here"}}
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: unknown, IfMatchVersion: version,
		},
	); err == nil {
		t.Fatal("unknown member must be refused")
	} else if werr, ok := worksetusecase.AsError(
		err,
	); !ok ||
		werr.Code != "UNKNOWN_MEMBER" {
		t.Fatalf("want UNKNOWN_MEMBER, got %v", err)
	}

	dup := draftDoc()
	dup.Members = []worksetusecase.DraftMember{
		{MemberID: ws.Members[0].MemberID},
		{MemberID: ws.Members[0].MemberID},
	}
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: dup, IfMatchVersion: version,
		},
	); err == nil {
		t.Fatal("duplicate member must be refused")
	} else if werr, ok := worksetusecase.AsError(
		err,
	); !ok ||
		werr.Code != "DUPLICATE_MEMBER" {
		t.Fatalf("want DUPLICATE_MEMBER, got %v", err)
	}

	// Nothing was partially written: the operation version is untouched.
	if got := f.operation(ws.WorksetID).Version; got != version {
		t.Fatalf("failed save advanced the version: %d -> %d", version, got)
	}
}

// TestDraftRejectsUnknownCodecAndMode covers the structural literal checks.
func TestDraftRejectsUnknownCodecAndMode(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("字段校验", ids...)
	version := ws.Operations[0].Version

	badMode := draftDoc()
	badMode.Mode = "yolo"
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: badMode, IfMatchVersion: version,
		},
	); err == nil {
		t.Fatal("unknown mode must be refused")
	}

	badCodec := draftDoc()
	badCodec.Matched.Lossless.Codec = "opus"
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: badCodec, IfMatchVersion: version,
		},
	); err == nil {
		t.Fatal("unknown codec must be refused")
	}

	// An undeclared output is incomplete, not malformed: it saves.
	undeclared := draftDoc()
	undeclared.Matched.Lossless = nil
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: undeclared, IfMatchVersion: version,
		},
	); err != nil {
		t.Fatalf("an incomplete output profile must be saveable: %v", err)
	}
}

// TestDraftEditRejectedWhileGenerating covers D05/E09 at the service boundary:
// a queued session freezes the draft against edits, and only this operation's
// draft is affected.
func TestDraftEditRejectedWhileGenerating(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("生成中", ids...)
	version := f.operation(ws.WorksetID).Version

	_, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: version, IdempotencyKey: "gen-freeze",
		},
	)
	if err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	if _, err := f.svc.SaveDraft(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document: draftDoc(), IfMatchVersion: version,
		},
	); err == nil {
		t.Fatal("draft edit during an active session must fail")
	} else if werr, ok := worksetusecase.AsError(
		err,
	); !ok ||
		werr.Code != "GENERATION_IN_PROGRESS" {
		t.Fatalf("want GENERATION_IN_PROGRESS, got %v", err)
	}
}
