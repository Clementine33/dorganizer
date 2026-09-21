package workset_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

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

// TestExecutionAtScaleRecordsEveryComponentOnce runs a session wide enough to
// page and checks the three things a per-component write path can get wrong:
// every component's result exists exactly once, the counters count the results
// rather than the writes, and a page is a window onto the component order
// rather than a second list with its own rules.
func TestExecutionAtScaleRecordsEveryComponentOnce(t *testing.T) {
	const components = 500
	plans := make([]poolPlan, components)
	seeds := make([]seedComponent, components)
	for i := range components {
		plans[i] = poolUnit(0)
		seeds[i] = poolComponent("u"+strconv.Itoa(i), "albumA")
	}
	f, _ := poolFixture(t, 2, plans...)
	f.seedRevision("plan-scale", seeds...)

	started := f.mustStart("plan-scale", "k-scale")
	done := f.waitTerminal(started.ExecutionID)
	if done.Status != "succeeded" {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if done.TotalComponents != components || done.CompletedComponents != components {
		t.Fatalf("counters = %d/%d, want %d/%d", done.CompletedComponents,
			done.TotalComponents, components, components)
	}
	if len(done.Components) != components {
		t.Fatalf("the detail carried %d of %d components", len(done.Components), components)
	}
	seen := make(map[int]struct{}, components)
	for _, component := range done.Components {
		if _, dupe := seen[component.ComponentIndex]; dupe {
			t.Fatalf("component %d appears twice", component.ComponentIndex)
		}
		seen[component.ComponentIndex] = struct{}{}
		if component.Status != worksetusecase.ExecComponentSucceeded {
			t.Fatalf("component %d status = %s", component.ComponentIndex, component.Status)
		}
	}

	// One row per component, and a page of the component list is the same
	// components the unpaged read carries, in the same order.
	rows, err := f.repo.ListExecutionComponentResults(started.ExecutionID, 0, 0)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(rows) != components {
		t.Fatalf("%d result rows for %d components", len(rows), components)
	}
	page, err := f.svc.GetExecution(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, started.ExecutionID,
		worksetusecase.ExecutionPage{FromIndex: 200, Limit: 200},
	)
	if err != nil {
		t.Fatalf("GetExecution(page): %v", err)
	}
	if len(page.Components) != 200 || page.Components[0].ComponentIndex != 200 ||
		page.Components[199].ComponentIndex != 399 {
		t.Fatalf("page = %d components, first %d, last %d", len(page.Components),
			page.Components[0].ComponentIndex, page.Components[len(page.Components)-1].ComponentIndex)
	}
	for i, component := range page.Components {
		if component.ComponentID != done.Components[200+i].ComponentID {
			t.Fatalf("page component %d = %s, want %s", i, component.ComponentID,
				done.Components[200+i].ComponentID)
		}
	}
}
