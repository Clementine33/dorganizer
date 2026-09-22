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

	"github.com/onsei/organizer/backend/internal/conversion"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
	"github.com/onsei/organizer/backend/internal/workset"
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
	return &poolTask{stubTask{kind: workset.OperationTypeConversion}, plans, obs}
}

// SeedDraft seeds a real conversion draft, so the fixture's revision seeding
// and the draft-drift gate agree on its canonical hash.
func (*poolTask) SeedDraft() ([]byte, string, int) {
	doc, err := conversion.ParseDraft(`{"schema_version":1,"classifier_tags":[]}`)
	if err != nil {
		panic(err)
	}
	raw, hash, marshalErr := conversion.MarshalDraft(doc)
	if marshalErr != nil {
		panic(marshalErr)
	}
	return []byte(raw), hash, conversion.DraftSchemaVersion
}

func (t *poolTask) FreezeExecution(
	in workset.RevisionFacts,
) (workset.FrozenExecution, []string, error) {
	frozen := workset.FrozenExecution{Options: json.RawMessage(`{}`)}
	if in.Detail == nil {
		return frozen, nil, nil
	}
	for i, c := range in.Detail.Components {
		frozen.Units = append(frozen.Units, workset.ExecutionUnit{
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
	in workset.UnitRunInput,
) (workset.PreparedUnit, error) {
	var plan poolPlan
	if err := json.Unmarshal(in.Unit.Payload, &plan); err != nil {
		return nil, workset.NewError(workset.ErrKindInternal, "PAYLOAD_INVALID", err.Error(), err)
	}
	t.obs.recordf("prepare %s", in.Unit.ID)
	if plan.FailPrepare {
		return nil, workset.NewError(
			workset.ErrKindPrecondition, "PREPARE_REFUSED", "prepare refused", nil,
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
		return workset.NewError(
			workset.ErrKindInternal, "ENCODE_FAILED", "encode refused", nil,
		).WithStage("materialize")
	}
	return nil
}

func (u *scriptUnit) Commit(ctx context.Context) (workset.UnitResult, error) {
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
	return workset.UnitResult{
		Committed: []string{u.id + "/out"},
		Removed:   []string{},
		Remaining: []string{},
		Recovery:  []string{},
	}, nil
}

// canceledFacts is a commit stopped between its operations: the partial facts
// of what already landed survive.
func (u *scriptUnit) canceledFacts() workset.UnitResult {
	return workset.UnitResult{
		Canceled:  true,
		Stage:     "commit",
		Committed: []string{u.id + "/a"},
		Removed:   []string{},
		Remaining: []string{u.id + "/b"},
		Recovery:  []string{},
	}
}

func (u *scriptUnit) Discard(cause error) workset.UnitResult {
	u.obs.recordf("discard %s", u.id)
	u.obs.leaveOpen()
	res := workset.UnitResult{
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
		t, nil, encodeConcurrency, []workset.Task{newPoolTask(obs, plans...)},
	)
	f.runDispatcher()
	return f, obs
}

// entryOf returns one component's report entry by frozen unit id.
func entryOf(t *testing.T, done *workset.ExecutionView, id string) *workset.ExecutionComponentView {
	t.Helper()
	for i := range done.Components {
		if done.Components[i].ComponentID == id {
			return &done.Components[i]
		}
	}
	t.Fatalf("component %s missing from the report: %+v", id, done.Components)
	return nil
}

// waitCurrentComponent polls the session until it names the given component.
func (f *execFixture) waitCurrentComponent(
	t *testing.T,
	executionID, componentID string,
) *workset.ExecutionView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		view, err := f.svc.GetExecution(
			f.t.Context(), f.worksetID, workset.OperationTypeConversion, executionID,
			workset.ExecutionPage{},
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
