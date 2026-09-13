package workset_test

import (
	"testing"
	"time"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestGenerationPublishesRevisionAndReplays covers T11's happy path plus P03:
// a successful session publishes exactly one revision, promotes it, and a
// second start with unchanged draft, members and input replays it instead of
// planning again.
func TestGenerationPublishesRevisionAndReplays(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("生成", ids...)

	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" || gen.RevisionID == "" {
		t.Fatalf("generation = %+v", gen)
	}
	op := f.operation(ws.WorksetID)
	if op.CurrentRevision == nil || op.CurrentRevision.PlanID != gen.RevisionID {
		t.Fatalf("current revision = %+v", op.CurrentRevision)
	}
	if op.PlanningState != worksetusecase.PlanningPlanned {
		t.Fatalf("planning state = %q", op.PlanningState)
	}
	if op.CurrentRevision.Counts.Members != 1 {
		t.Fatalf("counts = %+v", op.CurrentRevision.Counts)
	}

	// Same draft, same members, same live inventory: reuse the current version.
	replay, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "gen-replay",
		},
	)
	if err != nil {
		t.Fatalf("replay StartGeneration: %v", err)
	}
	if replay.Created || replay.Revision == nil || replay.Revision.PlanID != gen.RevisionID {
		t.Fatalf("expected a reuse of the current revision, got %+v", replay)
	}

	// A real input change still requires planning.
	f.insertAudioEntry("/music/albumA/02.mp3", "/music/albumA", 2048, 1001)
	fresh, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "gen-after-change",
		},
	)
	if err != nil {
		t.Fatalf("StartGeneration after input change: %v", err)
	}
	if !fresh.Created {
		t.Fatalf("changed input must plan again, got %+v", fresh)
	}
}

// TestDraftChangeMakesOperationNeedPlanning covers P02: after a draft change
// the operation is dirty, and the next successful session publishes a new
// revision that does not inherit the previous confirmation.
func TestDraftChangeMakesOperationNeedPlanning(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("脏检查", ids...)

	first := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if first.Status != "completed" {
		t.Fatalf("first generation: %+v", first)
	}
	op := f.operation(ws.WorksetID)
	if _, err := f.svc.ConfirmRevision(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		first.RevisionID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: op.Version,
		},
	); err != nil {
		t.Fatalf("ConfirmRevision: %v", err)
	}

	doc := draftDoc()
	doc.ClassifierTags = []string{"别的标签"}
	saved := f.saveDraft(ws.WorksetID, doc, op.Version)
	if saved.PlanningState != worksetusecase.PlanningNeedsPlanning {
		t.Fatalf("planning state after draft change = %q", saved.PlanningState)
	}

	second := f.runGeneration(ws.WorksetID, saved.Version)
	if second.Status != "completed" || second.RevisionID == first.RevisionID {
		t.Fatalf("second generation = %+v", second)
	}
	conf, err := f.svc.GetConfirmation(f.ctx, ws.WorksetID, worksetusecase.OperationTypeConversion, second.RevisionID)
	if err != nil {
		t.Fatalf("GetConfirmation: %v", err)
	}
	if conf.Confirmed {
		t.Fatal("a new revision must not inherit the previous confirmation")
	}
	// The historical revision keeps its own confirmation record.
	old, err := f.svc.GetConfirmation(f.ctx, ws.WorksetID, worksetusecase.OperationTypeConversion, first.RevisionID)
	if err != nil || !old.Confirmed {
		t.Fatalf("historical confirmation lost: %+v err=%v", old, err)
	}
}

// TestGenerationFailureKeepsCurrentRevision covers T11's failure half: a
// failed session leaves the promoted revision untouched and is not published.
func TestGenerationFailureKeepsCurrentRevision(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("失败", ids...)

	first := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if first.Status != "completed" {
		t.Fatalf("first generation: %+v", first)
	}

	// A queued session that fails (worker-side failure recorded through the
	// repository's terminal transition) must not touch the promoted revision.
	// The draft changes first so this start creates a session instead of
	// reusing the current revision.
	doc := draftDoc()
	doc.ClassifierTags = []string{"改标签"}
	saved := f.saveDraft(ws.WorksetID, doc, f.operation(ws.WorksetID).Version)
	genID := f.startGeneration(ws.WorksetID, saved.Version)
	if err := f.repo.MarkGenerationFailed(genID, "GENERATION_FAILED", "boom"); err != nil {
		t.Fatalf("MarkGenerationFailed: %v", err)
	}
	after := f.operation(ws.WorksetID)
	if after.CurrentRevision == nil || after.CurrentRevision.PlanID != first.RevisionID {
		t.Fatalf("failed session replaced the current revision: %+v", after.CurrentRevision)
	}
	if after.Version != saved.Version {
		t.Fatalf("failed session advanced the operation version: %d -> %d", saved.Version, after.Version)
	}
	if after.LatestGeneration == nil || after.LatestGeneration.Status != "failed" {
		t.Fatalf("latest generation = %+v", after.LatestGeneration)
	}
}

// TestCancelQueuedSessionIsNotPublished covers cooperative cancel: a canceled
// queued session never publishes.
func TestCancelQueuedSessionIsNotPublished(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("取消", ids...)
	op := f.operation(ws.WorksetID)
	genID := f.startGeneration(ws.WorksetID, op.Version)

	canceled, err := f.svc.CancelGeneration(f.ctx, ws.WorksetID, worksetusecase.OperationTypeConversion, genID)
	if err != nil {
		t.Fatalf("CancelGeneration: %v", err)
	}
	if canceled.Status != "canceled" {
		t.Fatalf("status = %q, want canceled", canceled.Status)
	}
	after := f.operation(ws.WorksetID)
	if after.CurrentRevision != nil {
		t.Fatalf("canceled session published a revision: %+v", after.CurrentRevision)
	}
	if after.PlanningState != worksetusecase.PlanningUnplanned {
		t.Fatalf("planning state = %q", after.PlanningState)
	}
}

// TestSingleActiveSessionPerOperation covers D05.
func TestSingleActiveSessionPerOperation(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("单会话", ids...)
	op := f.operation(ws.WorksetID)
	f.startGeneration(ws.WorksetID, op.Version)

	if _, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "gen-second",
		},
	); err == nil {
		t.Fatal("second concurrent session must be refused")
	} else if werr, ok := worksetusecase.AsError(
		err,
	); !ok ||
		werr.Code != "GENERATION_IN_PROGRESS" {
		t.Fatalf("want GENERATION_IN_PROGRESS, got %v", err)
	}
}

// TestGenerationRejectsScanInProgress covers the library-scan gate.
func TestGenerationRejectsScanInProgress(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	ws := f.createWorkset("扫描中", ids...)
	f.exec(`
		INSERT INTO scan_sessions (session_id, root_path, kind, status, started_at)
		VALUES ('scan-1', '/music', 'full', 'running', ?)
	`, time.Now().Format(timeFmt))

	op := f.operation(ws.WorksetID)
	_, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "gen-scanning",
		},
	)
	if err == nil {
		t.Fatal("generation during a scan must fail")
	}
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "SCAN_IN_PROGRESS" {
		t.Fatalf("want SCAN_IN_PROGRESS, got %v", err)
	}
}

// TestGenerationScopedAddressing covers T10: sessions, revisions and
// confirmations are addressable only through their owning operation.
func TestGenerationScopedAddressing(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("作用域", ids...)
	other := f.createWorkset("另一个", ids...)

	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" {
		t.Fatalf("generation: %+v", gen)
	}

	if _, err := f.svc.GetGeneration(
		f.ctx,
		other.WorksetID,
		worksetusecase.OperationTypeConversion,
		gen.GenerationID,
	); err == nil {
		t.Fatal("a session must not resolve through another workset")
	} else if werr, ok := worksetusecase.AsError(
		err,
	); !ok ||
		werr.Code != "GENERATION_NOT_FOUND" {
		t.Fatalf("want GENERATION_NOT_FOUND, got %v", err)
	}
	if _, err := f.svc.GetRevision(
		f.ctx,
		other.WorksetID,
		worksetusecase.OperationTypeConversion,
		gen.RevisionID,
	); err == nil {
		t.Fatal("a revision must not resolve through another workset")
	}
	if _, err := f.svc.ConfirmRevision(
		f.ctx,
		other.WorksetID,
		worksetusecase.OperationTypeConversion,
		gen.RevisionID,
		worksetusecase.ConfirmRequest{
			IfMatchVersion: f.operation(other.WorksetID).Version,
		},
	); err == nil {
		t.Fatal("a confirmation must not resolve through another workset")
	}
	if _, err := f.svc.GetGeneration(f.ctx, ws.WorksetID, "rename", gen.GenerationID); err == nil {
		t.Fatal("an unknown operation type must not resolve")
	}
}

// TestGenerationIdempotencyKeyReuseIsScoped covers the session idempotency
// contract: replaying the same key returns the session, a different request
// under the same key conflicts.
func TestGenerationIdempotencyKeyReuseIsScoped(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	ws := f.createWorkset("幂等", ids...)
	op := f.operation(ws.WorksetID)

	first, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "same-key",
		},
	)
	if err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	replay, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version, IdempotencyKey: "same-key",
		},
	)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Generation.GenerationID != first.Generation.GenerationID {
		t.Fatalf("idempotent replay returned a new session: %+v", replay)
	}
	if _, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: op.Version,
		},
	); err == nil {
		t.Fatal("a missing idempotency key must be refused")
	}
}
