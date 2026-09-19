package workset_test

import (
	"testing"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestStaleValidationIgnoresNonAudioChanges covers the revision validation
// contract: only recognized audio changes make a revision stale; sidecars and
// added non-audio files never do.
func TestStaleValidationIgnoresNonAudioChanges(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	f.insertNonAudioEntry("/music/albumA/cover.jpg", "/music/albumA", 2048, 1000)
	ws := f.createCurrent("测试正交", ids...)

	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" {
		t.Fatalf("generation: %+v", gen)
	}
	rev := f.operation(ws.WorksetID).CurrentRevision
	if rev == nil {
		t.Fatal("expected a current revision")
	}
	if rev.ValidationState != worksetusecase.ValidationValid || rev.Stale == nil || *rev.Stale {
		t.Fatalf("expected a valid, non-stale revision, got state=%q stale=%v", rev.ValidationState, rev.Stale)
	}

	// Non-audio churn must not invalidate the plan.
	f.exec("UPDATE entries SET mtime = mtime + 500 WHERE path = '/music/albumA/cover.jpg'")
	f.insertNonAudioEntry("/music/albumA/notes.txt", "/music/albumA", 500, 1000)
	afterSidecar := f.operation(ws.WorksetID).CurrentRevision
	if afterSidecar.ValidationState != worksetusecase.ValidationValid || *afterSidecar.Stale {
		t.Fatalf("non-audio change marked the revision stale: state=%q", afterSidecar.ValidationState)
	}

	// Audio churn does.
	f.exec("UPDATE entries SET mtime = mtime + 100 WHERE path = '/music/albumA/01.mp3'")
	afterAudio := f.operation(ws.WorksetID).CurrentRevision
	if afterAudio.ValidationState != worksetusecase.ValidationStale || !*afterAudio.Stale {
		t.Fatalf("audio change must mark the revision stale: state=%q", afterAudio.ValidationState)
	}
}

// TestMissingRootIsBlockedButNotStaleUntilAudioAppears covers the missing-root
// accounting: a member folder absent from the inventory is a blocked fact of
// the revision, not a stale input, until audio actually appears there.
func TestMissingRootIsBlockedButNotStaleUntilAudioAppears(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumMissing")
	ws := f.createCurrent("缺失根测试", ids...)

	// The directory is gone from the disk, so the next scan of the library
	// drops its subtree from the inventory. The record keeps its member: the
	// scope is fixed, the facts about it are not.
	f.exec("DELETE FROM entries WHERE path = '/music/albumMissing' OR path LIKE '/music/albumMissing/%'")

	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" {
		t.Fatalf("generation: %+v", gen)
	}
	rev := f.operation(ws.WorksetID).CurrentRevision
	if rev == nil {
		t.Fatal("expected a current revision")
	}
	if rev.Counts.Blocked == 0 {
		t.Fatalf("missing root must count as blocked: %+v", rev.Counts)
	}
	if rev.ValidationState != worksetusecase.ValidationValid || *rev.Stale {
		t.Fatalf("missing root with unchanged inventory must not be stale: state=%q", rev.ValidationState)
	}

	f.insertAudioEntry("/music/albumMissing/01.mp3", "/music/albumMissing", 1024, 1000)
	after := f.operation(ws.WorksetID).CurrentRevision
	if after.ValidationState != worksetusecase.ValidationStale || !*after.Stale {
		t.Fatalf("after audio appears the revision must be stale: state=%q", after.ValidationState)
	}
}

// TestOnlyTheCurrentPlanSurvivesTheNextGeneration covers R3: generating a new
// plan publishes it and retires the one it replaced, in that order — a record
// keeps one plan, and a failed or canceled generation never touches it.
func TestOnlyTheCurrentPlanSurvivesTheNextGeneration(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createCurrent("当前计划", ids...)
	f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	firstPlan := f.operation(ws.WorksetID).CurrentRevision.PlanID

	doc := draftDoc()
	doc.ClassifierTags = []string{"第二个"}
	saved := f.saveDraft(ws.WorksetID, doc, f.operation(ws.WorksetID).Version)
	if _, err := f.svc.GetRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		firstPlan,
	); err != nil {
		t.Fatalf("the old plan must stay readable while the new one is being generated: %v", err)
	}
	f.runGeneration(ws.WorksetID, saved.Version)

	secondPlan := f.operation(ws.WorksetID).CurrentRevision.PlanID
	if secondPlan == firstPlan {
		t.Fatal("the new generation must publish a new plan")
	}
	if _, err := f.svc.GetRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		firstPlan,
	); err == nil {
		t.Fatal("the replaced plan must not be readable")
	}
	var plans int
	f.queryInt(&plans, "SELECT COUNT(*) FROM plans WHERE workset_id = ?", ws.WorksetID)
	if plans != 1 {
		t.Fatalf("a record must keep exactly one plan, found %d", plans)
	}
}
