package workset_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// poolPlan is one frozen unit's script in the pool tests. Every unit declares
// its own encode tasks; FailEncode is the task index that fails (-1 for none).
type poolPlan struct {
	Tasks        int      `json:"tasks"`
	FailEncode   int      `json:"fail_encode"`
	FailPrepare  bool     `json:"fail_prepare"`
	CancelCommit bool     `json:"cancel_commit"`
	CommitGate   bool     `json:"commit_gate"`
	FinishOnStop bool     `json:"finish_on_stop"`
	Leftovers    []string `json:"leftovers"`
}

// poolUnit is the standard script: n encode tasks and nothing else.
func poolUnit(tasks int) poolPlan {
	return poolPlan{Tasks: tasks, FailEncode: -1}
}

// poolComponent seeds one frozen component of the given member folder.
func poolComponent(id, member string) seedComponent {
	return seedComponent{id: id, member: member, partition: reconcile.PartitionMatched}
}

// poolObservations is the shared, thread-safe log and gate of one pool test:
// every lifecycle event in order, the encodes running at once and the units
// open at once.
type poolObservations struct {
	mu         sync.Mutex
	events     []string
	gates      map[string]chan struct{}
	open       int
	maxOpen    int
	running    int
	maxRunning int
}

func newPoolObservations() *poolObservations {
	return &poolObservations{gates: map[string]chan struct{}{}}
}

// gate makes the unit's encodes wait until the test releases them.
func (o *poolObservations) gate(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gates[id] = make(chan struct{})
}

// release lets a gated unit's encodes return.
func (o *poolObservations) release(id string) {
	o.mu.Lock()
	gate, ok := o.gates[id]
	delete(o.gates, id)
	o.mu.Unlock()
	if ok {
		close(gate)
	}
}

func (o *poolObservations) recordf(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, fmt.Sprintf(format, args...))
}

func (o *poolObservations) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.events)
}

// enterEncode records one encode start and waits for the unit's gate, the
// session's cancellation or the test's release. finishOnStop models the one
// encoder that was past its last write when the stop landed.
func (o *poolObservations) enterEncode(ctx context.Context, id string, index int, finishOnStop bool) error {
	o.mu.Lock()
	o.running++
	o.maxRunning = max(o.maxRunning, o.running)
	gate := o.gates[id]
	o.mu.Unlock()
	o.recordf("encode %s#%d", id, index)
	defer func() {
		o.mu.Lock()
		o.running--
		o.mu.Unlock()
	}()
	if gate == nil {
		return nil
	}
	if finishOnStop {
		<-gate
		return nil
	}
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enterOpen records a unit prepared-and-uncommitted; leaveOpen closes it.
func (o *poolObservations) enterOpen() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.open++
	o.maxOpen = max(o.maxOpen, o.open)
}

func (o *poolObservations) leaveOpen() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.open--
}

// waitEncode blocks until the named unit's first encode has started.
func (o *poolObservations) waitEncode(t *testing.T, id string) {
	t.Helper()
	o.waitEvent(t, fmt.Sprintf("encode %s#0", id))
}

// waitEvent blocks until one lifecycle event has been recorded.
func (o *poolObservations) waitEvent(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(o.snapshot(), want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("event %q never appeared; events %v", want, o.snapshot())
}

// indexOf returns the position of an event, or -1.
func indexOf(events []string, want string) int {
	return slices.Index(events, want)
}

// poolTask is the controllable task of the pool tests: it plans one unit per
// frozen component into the scripted payloads, and each unit runs exactly what
// its script says.
type poolTask struct {
	stubTask

	plans []poolPlan
	obs   *poolObservations
}

// newPoolTask scripts one session: one plan per frozen component, in order.
func newPoolTask(obs *poolObservations, plans ...poolPlan) *poolTask {
	return &poolTask{stubTask{kind: worksetusecase.OperationTypeConversion}, plans, obs}
}

// SeedDraft seeds a real conversion draft, so the fixture's revision seeding
// and the draft-drift gate agree on its canonical hash.
func (*poolTask) SeedDraft() ([]byte, string, int) {
	doc, err := tasksconversion.ParseDraft(`{"schema_version":1,"classifier_tags":[]}`)
	if err != nil {
		panic(err)
	}
	raw, hash, marshalErr := tasksconversion.MarshalDraft(doc)
	if marshalErr != nil {
		panic(marshalErr)
	}
	return []byte(raw), hash, tasksconversion.DraftSchemaVersion
}

func (t *poolTask) FreezeExecution(
	_ *sqlite.Repository,
	in worksetusecase.RevisionFacts,
) (worksetusecase.FrozenExecution, []string, error) {
	frozen := worksetusecase.FrozenExecution{Options: json.RawMessage(`{}`)}
	if in.Detail == nil {
		return frozen, nil, nil
	}
	for i, c := range in.Detail.Components {
		frozen.Units = append(frozen.Units, worksetusecase.ExecutionUnit{
			Index:     c.ComponentIndex,
			RootIndex: c.RootIndex,
			ID:        c.ComponentID,
			Partition: c.Partition,
			Payload:   json.RawMessage(mustPoolJSON(t.plans[i])),
		})
	}
	return frozen, nil, nil
}

func (t *poolTask) PrepareUnit(
	_ context.Context,
	_ *sqlite.Repository,
	in worksetusecase.UnitRunInput,
) (worksetusecase.PreparedUnit, error) {
	var plan poolPlan
	if err := json.Unmarshal(in.Unit.Payload, &plan); err != nil {
		return nil, worksetusecase.NewError(worksetusecase.ErrKindInternal, "PAYLOAD_INVALID", err.Error(), err)
	}
	t.obs.recordf("prepare %s", in.Unit.ID)
	if plan.FailPrepare {
		return nil, worksetusecase.NewError(
			worksetusecase.ErrKindPrecondition, "PREPARE_REFUSED", "prepare refused", nil,
		).WithStage("precheck")
	}
	t.obs.enterOpen()
	return &scriptUnit{id: in.Unit.ID, plan: plan, obs: t.obs}, nil
}

// scriptUnit is one prepared pool unit.
type scriptUnit struct {
	id   string
	plan poolPlan
	obs  *poolObservations
}

func (u *scriptUnit) EncodeTasks() int { return u.plan.Tasks }

func (u *scriptUnit) EncodeTask(ctx context.Context, index int) error {
	err := u.obs.enterEncode(ctx, u.id, index, u.plan.FinishOnStop)
	u.obs.recordf("return %s#%d", u.id, index)
	if err != nil {
		return err
	}
	if u.plan.FailEncode == index {
		return worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "ENCODE_FAILED", "encode refused", nil,
		).WithStage("materialize")
	}
	return nil
}

func (u *scriptUnit) Commit(ctx context.Context) (worksetusecase.UnitResult, error) {
	u.obs.recordf("commit %s", u.id)
	defer u.obs.leaveOpen()
	if u.plan.CommitGate {
		// The coordinator stays inside this commit while a sibling fails: the
		// shared context reaches it between the commit's operations.
		<-ctx.Done()
		return u.canceledFacts(), nil
	}
	if u.plan.CancelCommit {
		return u.canceledFacts(), nil
	}
	return worksetusecase.UnitResult{
		Committed: []string{u.id + "/out"},
		Removed:   []string{},
		Remaining: []string{},
		Recovery:  []string{},
	}, nil
}

// canceledFacts is a commit stopped between its operations: the partial facts
// of what already landed survive.
func (u *scriptUnit) canceledFacts() worksetusecase.UnitResult {
	return worksetusecase.UnitResult{
		Canceled:  true,
		Stage:     "commit",
		Committed: []string{u.id + "/a"},
		Removed:   []string{},
		Remaining: []string{u.id + "/b"},
		Recovery:  []string{},
	}
}

func (u *scriptUnit) Discard(cause error) worksetusecase.UnitResult {
	u.obs.recordf("discard %s", u.id)
	u.obs.leaveOpen()
	res := worksetusecase.UnitResult{
		Committed: []string{},
		Removed:   []string{},
		Remaining: []string{u.id + "/staged"},
		Recovery:  append([]string{}, u.plan.Leftovers...),
	}
	if cause == nil {
		res.Canceled = true
		return res
	}
	res.Stage, res.ErrorCode, res.ErrorMessage = "materialize", "ENCODE_FAILED", "encode refused"
	return res
}

func mustPoolJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// poolFixture wires one execution-ready fixture over a scripted pool task.
func poolFixture(t *testing.T, encodeConcurrency int, plans ...poolPlan) (*execFixture, *poolObservations) {
	t.Helper()
	obs := newPoolObservations()
	f := newExecFixtureWithTask(
		t, nil, encodeConcurrency, []worksetusecase.Task{newPoolTask(obs, plans...)},
	)
	f.runDispatcher()
	return f, obs
}

// entryOf returns one component's report entry by frozen unit id.
func entryOf(t *testing.T, done *worksetusecase.ExecutionView, id string) *worksetusecase.ExecutionComponentView {
	t.Helper()
	for i := range done.Components {
		if done.Components[i].ComponentID == id {
			return &done.Components[i]
		}
	}
	t.Fatalf("component %s missing from the report: %+v", id, done.Components)
	return nil
}

// TestExecutionPoolEncodesConcurrentlyAndCommitsInFrozenOrder drives two units
// through the shared pool: their encodes overlap, the later one finishes
// first, and the commits still follow the frozen order.
func TestExecutionPoolEncodesConcurrentlyAndCommitsInFrozenOrder(t *testing.T) {
	f, obs := poolFixture(t, 2, poolUnit(1), poolUnit(1))
	obs.gate("u0")
	obs.gate("u1")
	f.seedRevision("plan-pool", poolComponent("u0", "albumA"), poolComponent("u1", "albumB"))

	started := f.mustStart("plan-pool", "k-pool")
	obs.waitEncode(t, "u0")
	obs.waitEncode(t, "u1") // both encodes are running before either is released
	obs.release("u1")
	obs.waitEvent(t, "return u1#0") // the later unit finishes first
	obs.release("u0")
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "succeeded" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	events := obs.snapshot()
	if obs.maxRunning != 2 {
		t.Fatalf("encode concurrency = %d, want two encodes at once (events %v)", obs.maxRunning, events)
	}
	if indexOf(events, "return u1#0") > indexOf(events, "return u0#0") {
		t.Fatalf("the second unit must finish first: %v", events)
	}
	if indexOf(events, "commit u0") > indexOf(events, "commit u1") ||
		indexOf(events, "commit u1") < 0 {
		t.Fatalf("commits must follow the frozen order: %v", events)
	}
	for _, id := range []string{"u0", "u1"} {
		entry := entryOf(t, done, id)
		if entry.Status != "succeeded" || len(entry.Committed) != 1 {
			t.Fatalf("component %s = %+v", id, entry)
		}
	}
}

// TestExecutionPoolNeverOpensMoreUnitsThanItsWindow pins the window: with two
// encode workers, a third component enters only after the first one committed,
// and zero-task components never stall the queue.
func TestExecutionPoolNeverOpensMoreUnitsThanItsWindow(t *testing.T) {
	f, obs := poolFixture(t, 2, poolUnit(1), poolUnit(0), poolUnit(0))
	obs.gate("u0")
	f.seedRevision(
		"plan-window",
		poolComponent("u0", "albumA"),
		poolComponent("u1", "albumB"),
		poolComponent("u2", "albumA"),
	)

	started := f.mustStart("plan-window", "k-window")
	obs.waitEncode(t, "u0")
	obs.release("u0")
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "succeeded" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	events := obs.snapshot()
	if obs.maxOpen != 2 {
		t.Fatalf("open units peaked at %d, want the window of 2 (events %v)", obs.maxOpen, events)
	}
	if indexOf(events, "prepare u2") < indexOf(events, "commit u0") {
		t.Fatalf("the third unit must wait for a window slot: %v", events)
	}
	var order []string
	for _, event := range events {
		if strings.HasPrefix(event, "commit ") {
			order = append(order, event)
		}
	}
	if !slices.Equal(order, []string{"commit u0", "commit u1", "commit u2"}) {
		t.Fatalf("commit order = %v, want the frozen order", order)
	}
	for _, id := range []string{"u0", "u1", "u2"} {
		entry := entryOf(t, done, id)
		if entry.Status != "succeeded" {
			t.Fatalf("component %s = %+v", id, entry)
		}
		if !slices.Equal(entry.Committed, []string{id + "/out"}) {
			t.Fatalf("component %s committed %v", id, entry.Committed)
		}
	}
}

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

// waitCurrentComponent polls the session until it names the given component.
func (f *execFixture) waitCurrentComponent(
	t *testing.T,
	executionID, componentID string,
) *worksetusecase.ExecutionView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		view, err := f.svc.GetExecution(
			f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, executionID,
		)
		if err != nil {
			t.Fatalf("GetExecution: %v", err)
		}
		if view.CurrentComponentID == componentID {
			return view
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("component %s never became current", componentID)
	return nil
}

// TestExecutionPoolEmptySessionSucceeds covers the empty worklist: no frozen
// unit is indexed, no worker pool starts, and the session completes as it
// always did.
func TestExecutionPoolEmptySessionSucceeds(t *testing.T) {
	f, obs := poolFixture(t, 2)
	f.seedRevision("plan-empty")

	started := f.mustStart("plan-empty", "k-empty")
	done := f.waitTerminal(started.ExecutionID)

	if done.Status != "succeeded" || done.ErrorCode != "" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if done.TotalComponents != 0 || len(done.Components) != 0 {
		t.Fatalf("an empty session reported components: %+v", done.Components)
	}
	if events := obs.snapshot(); len(events) != 0 {
		t.Fatalf("an empty session must not start workers: %v", events)
	}
}

// TestExecutionPoolProgressMovesToTheNextUnitBeforeItBlocks pins the progress
// rule at N = 1: the next unit becomes the current one before its preparation
// or delivery can block, even though the window emptied after the commit.
func TestExecutionPoolProgressMovesToTheNextUnitBeforeItBlocks(t *testing.T) {
	f, obs := poolFixture(t, 1, poolUnit(1), poolUnit(1))
	obs.gate("u1")
	f.seedRevision("plan-progress", poolComponent("u0", "albumA"), poolComponent("u1", "albumB"))

	started := f.mustStart("plan-progress", "k-progress")
	obs.waitEvent(t, "commit u0")
	if view := f.waitCurrentComponent(t, started.ExecutionID, "u1"); view.Status != sqlite.ExecStatusRunning {
		t.Fatalf("the session must still be running while u1 encodes: %+v", view)
	}
	events := obs.snapshot()
	if indexOf(events, "encode u1#0") < 0 || indexOf(events, "return u1#0") >= 0 {
		t.Fatalf("u1 must be encoding, not finished: %v", events)
	}
	obs.release("u1")
	if done := f.waitTerminal(started.ExecutionID); done.Status != "succeeded" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
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

// lastIndex and firstIndex scan the event log for the last and first event
// with the given prefix.
func lastIndex(events []string, prefix string) int {
	for i, event := range slices.Backward(events) {
		if strings.HasPrefix(event, prefix) {
			return i
		}
	}
	return -1
}

func firstIndex(events []string, prefix string) int {
	for i, event := range events {
		if strings.HasPrefix(event, prefix) {
			return i
		}
	}
	return -1
}
