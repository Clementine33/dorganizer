package workset_test

import (
	"slices"
	"testing"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestExecutionPoolFailureStopsAndDiscardsTheOpenUnits covers the fail-fast
// teardown: the failing unit is reported failed, every other open unit is
// discarded with its leftovers recorded and stays pending, and a cancel
// request arriving afterwards never turns the failure into a cancellation.
func TestExecutionPoolFailureStopsAndDiscardsTheOpenUnits(t *testing.T) {
	failing := poolUnit(1)
	failing.FailEncode = 0
	inFlight := poolUnit(1)
	inFlight.Leftovers = []string{"/recovery/u0-tmp"}
	f, obs := poolFixture(t, 2, inFlight, failing, poolUnit(1))
	obs.gate("u0")
	f.seedRevision(
		"plan-fail",
		poolComponent("u0", "albumA"),
		poolComponent("u1", "albumB"),
		poolComponent("u2", "albumA"),
	)

	started := f.mustStart("plan-fail", "k-fail")
	obs.waitEncode(t, "u0")
	obs.waitEvent(t, "return u1#0")
	if _, err := f.svc.CancelExecution(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, started.ExecutionID,
	); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "failed" || done.ErrorCode != "ENCODE_FAILED" {
		t.Fatalf("cancel must not hide the failure: %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	failed := entryOf(t, done, "u1")
	if failed.Status != "failed" || failed.Stage != "materialize" || failed.ErrorCode != "ENCODE_FAILED" {
		t.Fatalf("failing component = %+v", failed)
	}
	pending := entryOf(t, done, "u0")
	if pending.Status != "pending" {
		t.Fatalf("a unit whose commit never began must stay pending: %+v", pending)
	}
	if !slices.Equal(pending.Remaining, []string{"u0/staged"}) {
		t.Fatalf("the unrun operations must stay visible: %+v", pending.Remaining)
	}
	if !slices.Equal(pending.Recovery, []string{"/recovery/u0-tmp"}) {
		t.Fatalf("leftovers must survive on the pending entry: %+v", pending.Recovery)
	}
	never := entryOf(t, done, "u2")
	if never.Status != "pending" {
		t.Fatalf("a unit never opened must stay pending: %+v", never)
	}
	events := obs.snapshot()
	lastReturn, firstDiscard := lastIndex(events, "return "), firstIndex(events, "discard ")
	if lastReturn < 0 || firstDiscard < 0 || lastReturn > firstDiscard {
		t.Fatalf("staged files must be cleaned only after every worker returned: %v", events)
	}
}

// TestExecutionPoolPrepareFailureIsAttributedToItsUnit covers the attribution
// rule: a window filled while A is the head may fail on B, and the error must
// land on B — with its own stage and code — never on the head.
func TestExecutionPoolPrepareFailureIsAttributedToItsUnit(t *testing.T) {
	head := poolUnit(1)
	head.Leftovers = []string{"/recovery/head-tmp"}
	refused := poolUnit(0)
	refused.FailPrepare = true
	f, obs := poolFixture(t, 2, head, refused, poolUnit(0))
	obs.gate("u0")
	f.seedRevision(
		"plan-prepare",
		poolComponent("u0", "albumA"),
		poolComponent("u1", "albumB"),
		poolComponent("u2", "albumA"),
	)

	started := f.mustStart("plan-prepare", "k-prepare")
	obs.waitEncode(t, "u0")
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "failed" || done.ErrorCode != "PREPARE_REFUSED" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	failed := entryOf(t, done, "u1")
	if failed.Status != "failed" || failed.Stage != "precheck" || failed.ErrorCode != "PREPARE_REFUSED" {
		t.Fatalf("the prepared unit owns the failure: %+v", failed)
	}
	headEntry := entryOf(t, done, "u0")
	if headEntry.Status != "pending" || headEntry.ErrorCode != "" {
		t.Fatalf("the head must not be blamed: %+v", headEntry)
	}
	if !slices.Equal(headEntry.Recovery, []string{"/recovery/head-tmp"}) {
		t.Fatalf("the head's leftovers must be recorded: %+v", headEntry.Recovery)
	}
	if entryOf(t, done, "u2").Status != "pending" {
		t.Fatalf("a unit never opened must stay pending: %+v", entryOf(t, done, "u2"))
	}
}

// TestExecutionPoolStopInsideCommitKeepsPartialFacts covers a stop that lands
// at the head's own commit: the head keeps its partial facts and is never
// discarded, while every other open unit stays pending.
func TestExecutionPoolStopInsideCommitKeepsPartialFacts(t *testing.T) {
	head := poolUnit(0)
	head.CancelCommit = true
	other := poolUnit(0)
	other.Leftovers = []string{"/recovery/other-tmp"}
	f, obs := poolFixture(t, 2, head, other)
	f.seedRevision("plan-commit", poolComponent("u0", "albumA"), poolComponent("u1", "albumB"))

	started := f.mustStart("plan-commit", "k-commit")
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "canceled" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	headEntry := entryOf(t, done, "u0")
	if headEntry.Status != "canceled" || headEntry.Stage != "commit" {
		t.Fatalf("the stopped head keeps its partial facts: %+v", headEntry)
	}
	if !slices.Equal(headEntry.Committed, []string{"u0/a"}) ||
		!slices.Equal(headEntry.Remaining, []string{"u0/b"}) {
		t.Fatalf("partial facts were overwritten: %+v", headEntry)
	}
	otherEntry := entryOf(t, done, "u1")
	if otherEntry.Status != "pending" {
		t.Fatalf("the open unit stays pending: %+v", otherEntry)
	}
	if !slices.Equal(otherEntry.Recovery, []string{"/recovery/other-tmp"}) {
		t.Fatalf("the open unit's leftovers must be recorded: %+v", otherEntry.Recovery)
	}
	events := obs.snapshot()
	if indexOf(events, "discard u0") >= 0 || indexOf(events, "discard u1") < 0 {
		t.Fatalf("only the open unit may be discarded: %v", events)
	}
}

// TestExecutionPoolCancellationLeavesPendingTails covers the user's stop: the
// session is canceled only when no real error was recorded, and every unit
// stops pending because nothing changed on disk.
func TestExecutionPoolCancellationLeavesPendingTails(t *testing.T) {
	f, obs := poolFixture(t, 2, poolUnit(1), poolUnit(1))
	obs.gate("u0")
	obs.gate("u1")
	f.seedRevision("plan-cancel", poolComponent("u0", "albumA"), poolComponent("u1", "albumB"))

	started := f.mustStart("plan-cancel", "k-cancel")
	obs.waitEncode(t, "u0")
	if _, err := f.svc.CancelExecution(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, started.ExecutionID,
	); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "canceled" || done.ErrorCode != "CANCELED" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	for _, id := range []string{"u0", "u1"} {
		entry := entryOf(t, done, id)
		if entry.Status != "pending" {
			t.Fatalf("component %s = %+v, want pending", id, entry)
		}
		if !slices.Equal(entry.Remaining, []string{id + "/staged"}) {
			t.Fatalf("component %s must still name its unrun operations: %+v", id, entry.Remaining)
		}
	}
	if events := obs.snapshot(); indexOf(events, "commit u0") >= 0 || indexOf(events, "commit u1") >= 0 {
		t.Fatalf("a canceled session must not commit: %v", events)
	}
}

// TestExecutionPoolStopReachesACommitInFlight proves the stop path needs no
// failure notification from the coordinator: a component is inside its commit
// while a sibling's encode fails, and the shared context stops it between its
// commit operations, keeping every partial fact it observed.
func TestExecutionPoolStopReachesACommitInFlight(t *testing.T) {
	inCommit := poolUnit(1)
	inCommit.CommitGate = true
	failing := poolUnit(1)
	failing.FailEncode = 0
	f, obs := poolFixture(t, 3, inCommit, failing, poolUnit(1))
	obs.gate("u1")
	obs.gate("u2")
	f.seedRevision(
		"plan-inflight",
		poolComponent("u0", "albumA"),
		poolComponent("u1", "albumB"),
		poolComponent("u2", "albumA"),
	)

	started := f.mustStart("plan-inflight", "k-inflight")
	obs.waitEvent(t, "commit u0") // the coordinator is inside u0's commit
	obs.release("u1")             // a sibling's encode fails meanwhile
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "failed" || done.ErrorCode != "ENCODE_FAILED" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	stopped := entryOf(t, done, "u0")
	if stopped.Status != "canceled" || stopped.Stage != "commit" {
		t.Fatalf("the stopped component keeps its partial facts: %+v", stopped)
	}
	if !slices.Equal(stopped.Committed, []string{"u0/a"}) ||
		!slices.Equal(stopped.Remaining, []string{"u0/b"}) {
		t.Fatalf("partial facts were not preserved: %+v", stopped)
	}
	if !stopped.InventorySynced {
		t.Fatalf("a stopped component still syncs its inventory: %+v", stopped)
	}
	failed := entryOf(t, done, "u1")
	if failed.Status != "failed" || failed.Stage != "materialize" {
		t.Fatalf("sibling = %+v", failed)
	}
	if open := entryOf(t, done, "u2"); open.Status != "pending" {
		t.Fatalf("the third component stays pending: %+v", open)
	}
	events := obs.snapshot()
	if indexOf(events, "discard u0") >= 0 || indexOf(events, "discard u2") < 0 {
		t.Fatalf("only units whose commit never began are discarded: %v", events)
	}
}

// TestExecutionPoolSealedHeadNeverCommitsAfterAStop covers completion racing
// the stop: the head's last task returns after the stop was recorded, so the
// session must not commit it even though completion and cancellation were both
// ready.
func TestExecutionPoolSealedHeadNeverCommitsAfterAStop(t *testing.T) {
	head := poolUnit(1)
	head.FinishOnStop = true
	failing := poolUnit(1)
	failing.FailEncode = 0
	f, obs := poolFixture(t, 2, head, failing)
	obs.gate("u0")
	obs.gate("u1")
	f.seedRevision("plan-race", poolComponent("u0", "albumA"), poolComponent("u1", "albumB"))

	started := f.mustStart("plan-race", "k-race")
	obs.waitEncode(t, "u0")
	obs.waitEncode(t, "u1")
	obs.release("u1") // the failure is recorded and the session stops
	obs.waitEvent(t, "return u1#0")
	obs.release("u0") // the head's own task finishes afterwards
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "failed" || done.ErrorCode != "ENCODE_FAILED" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if headEntry := entryOf(t, done, "u0"); headEntry.Status != "pending" {
		t.Fatalf("a stopped session must not commit the sealed head: %+v", headEntry)
	}
	if events := obs.snapshot(); indexOf(events, "commit u0") >= 0 {
		t.Fatalf("no commit may start after the stop: %v", events)
	}
}

// TestExecutionPoolStopReachesABlockedDelivery covers a stop while the queue
// is full and the only worker is busy: the cancel-aware send aborts instead of
// waiting the queue out, so the teardown never hangs behind a delivery.
func TestExecutionPoolStopReachesABlockedDelivery(t *testing.T) {
	f, obs := poolFixture(t, 1, poolUnit(3)) // one worker, one queue slot
	obs.gate("u0")
	f.seedRevision("plan-blocked", poolComponent("u0", "albumA"))

	started := f.mustStart("plan-blocked", "k-blocked")
	obs.waitEncode(t, "u0") // the worker is inside the gated encode
	if _, err := f.svc.CancelExecution(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, started.ExecutionID,
	); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "canceled" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	entry := entryOf(t, done, "u0")
	if entry.Status != "pending" || len(entry.Remaining) == 0 {
		t.Fatalf("the blocked unit stays pending with its unrun range: %+v", entry)
	}
	if events := obs.snapshot(); indexOf(events, "commit u0") >= 0 {
		t.Fatalf("a canceled session must not commit: %v", events)
	}
}
