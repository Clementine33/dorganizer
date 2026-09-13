package workset_test

import (
	"slices"
	"testing"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// confirmableFixture returns a workset with one completed revision and the
// operation view that follows it.
func confirmableFixture(t *testing.T) (*fixture, *worksetusecase.WorksetView, string) {
	t.Helper()
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("确认", ids...)
	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" {
		t.Fatalf("generation: %+v", gen)
	}
	return f, ws, gen.RevisionID
}

func reasonCodes(err error) []string {
	werr, ok := worksetusecase.AsError(err)
	if !ok {
		return nil
	}
	return werr.Details
}

func hasReason(codes []string, want string) bool {
	return slices.Contains(codes, want)
}

// TestConfirmRevisionLifecycle covers T14's positive path: the current version
// confirms (201 semantics), repeats idempotently, and a stale If-Match is a
// version conflict rather than a silent success.
func TestConfirmRevisionLifecycle(t *testing.T) {
	f, ws, planID := confirmableFixture(t)
	op := f.operation(ws.WorksetID)

	res, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		planID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: op.Version,
		},
	)
	if err != nil {
		t.Fatalf("ConfirmRevision: %v", err)
	}
	if !res.Created || !res.Confirmation.Confirmed {
		t.Fatalf("first confirmation = %+v", res)
	}
	repeat, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		planID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: op.Version,
		},
	)
	if err != nil {
		t.Fatalf("repeat ConfirmRevision: %v", err)
	}
	if repeat.Created {
		t.Fatalf("repeat confirmation must be idempotent: %+v", repeat)
	}
	if _, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		planID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: op.Version + 5,
		},
	); err == nil {
		t.Fatal("a stale If-Match must be refused")
	} else if werr, ok := worksetusecase.AsError(
		err,
	); !ok ||
		werr.Code != "VERSION_CONFLICT" {
		t.Fatalf("want VERSION_CONFLICT, got %v", err)
	}
}

// TestConfirmRejectsHistoricalRevision covers the current-version rule: an
// older revision can never be confirmed once a newer one is published.
func TestConfirmRejectsHistoricalRevision(t *testing.T) {
	f, ws, firstPlan := confirmableFixture(t)
	op := f.operation(ws.WorksetID)
	doc := draftDoc()
	doc.ClassifierTags = []string{"改过的标签"}
	saved := f.saveDraft(ws.WorksetID, doc, op.Version)
	second := f.runGeneration(ws.WorksetID, saved.Version)
	if second.Status != "completed" {
		t.Fatalf("second generation: %+v", second)
	}

	_, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		firstPlan,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: f.operation(ws.WorksetID).Version,
		},
	)
	if err == nil {
		t.Fatal("a historical revision must not be confirmable")
	}
	if !hasReason(reasonCodes(err), worksetusecase.ConfirmBlockedNotCurrent) {
		t.Fatalf("want NOT_CURRENT_REVISION, got %v", err)
	}
}

// TestConfirmRejectsChangedDraft covers the draft-drift rule: saving any draft
// edit revokes confirmability of the current revision until it is replanned.
func TestConfirmRejectsChangedDraft(t *testing.T) {
	f, ws, planID := confirmableFixture(t)
	op := f.operation(ws.WorksetID)
	doc := draftDoc()
	doc.ClassifierTags = []string{"另一个标签"}
	saved := f.saveDraft(ws.WorksetID, doc, op.Version)

	_, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		planID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: saved.Version,
		},
	)
	if err == nil {
		t.Fatal("a changed draft must block confirmation")
	}
	if !hasReason(reasonCodes(err), worksetusecase.ConfirmBlockedDraft) {
		t.Fatalf("want DRAFT_CHANGED, got %v", err)
	}
}

// TestConfirmRejectsChangedInput covers the input-freshness rule.
func TestConfirmRejectsChangedInput(t *testing.T) {
	f, ws, planID := confirmableFixture(t)
	f.exec("UPDATE entries SET mtime = mtime + 999 WHERE path = '/music/albumA/01.mp3'")

	_, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		planID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: f.operation(ws.WorksetID).Version,
		},
	)
	if err == nil {
		t.Fatal("a stale input must block confirmation")
	}
	if !hasReason(reasonCodes(err), worksetusecase.ConfirmBlockedInput) {
		t.Fatalf("want INPUT_CHANGED, got %v", err)
	}
}

// TestConfirmRejectsActiveGeneration covers the generation rule.
func TestConfirmRejectsActiveGeneration(t *testing.T) {
	f, ws, planID := confirmableFixture(t)
	// Change the draft so the next start plans again, then leave the session
	// queued: an active session blocks confirmation on its own.
	doc := draftDoc()
	doc.ClassifierTags = []string{"另一个标签"}
	saved := f.saveDraft(ws.WorksetID, doc, f.operation(ws.WorksetID).Version)
	f.startGeneration(ws.WorksetID, saved.Version)

	_, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		planID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: saved.Version,
		},
	)
	if err == nil {
		t.Fatal("an active session must block confirmation")
	}
	if !hasReason(reasonCodes(err), worksetusecase.ConfirmBlockedGeneration) {
		t.Fatalf("want GENERATION_IN_PROGRESS, got %v", err)
	}
}

// TestOrphanedWorksetCannotConfirm covers T15's confirmation half.
func TestOrphanedWorksetCannotConfirm(t *testing.T) {
	f, ws, planID := confirmableFixture(t)
	f.exec("UPDATE worksets SET library_id = NULL WHERE id = ?", ws.WorksetID)

	_, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		planID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: f.operation(ws.WorksetID).Version,
		},
	)
	if err == nil {
		t.Fatal("an orphaned workset must not be confirmable")
	}
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "ORPHANED_WORKSET" {
		t.Fatalf("want ORPHANED_WORKSET, got %v", err)
	}
}
