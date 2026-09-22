package workset

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
)

// dispatcher is a singleton global FIFO scheduler plus a fixed worker pool. It
// is not one goroutine per generation: workers claim sessions one at a time
// from the queue table in julianday(created_at), generation_id order.
//
// Execution sessions share the lifecycle but not the queue: one dedicated
// worker claims executions from their own table, so executions are globally
// serialized (first version) and a planning worker never consumes an execution
// wake.
type dispatcher struct {
	svc     *serviceImpl
	workers int

	wakeC     chan struct{}
	execWakeC chan struct{}
	done      chan struct{}
	stop      sync.Once
}

func newDispatcher(svc *serviceImpl, workers int) *dispatcher {
	if workers <= 0 {
		workers = 2
	}
	return &dispatcher{
		svc:       svc,
		workers:   workers,
		wakeC:     make(chan struct{}, 1),
		execWakeC: make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
}

// Start launches the worker pool. It is called once at process startup after
// InterruptStaleGenerations and InterruptStaleExecutions, so both queues are
// always clean.
func (d *dispatcher) Start() {
	for range d.workers {
		go d.run()
	}
	go d.runExecutionLoop()
}

// wake pokes the pool after a session is enqueued or a queued session is
// canceled, so workers re-check the queue immediately.
func (d *dispatcher) wake() {
	select {
	case d.wakeC <- struct{}{}:
	default:
	}
}

// wakeExecution pokes the execution worker after a session is enqueued or a
// queued session is canceled.
func (d *dispatcher) wakeExecution() {
	select {
	case d.execWakeC <- struct{}{}:
	default:
	}
}

// Stop shuts the pool down. In-flight generations and executions still running
// when Stop is called are allowed to finish; the next process start marks
// leftovers as interrupted.
func (d *dispatcher) Stop() {
	d.stop.Do(func() { close(d.done) })
}

func (d *dispatcher) run() {
	for {
		select {
		case <-d.done:
			return
		case <-d.wakeC:
		default:
		}
		gen, err := d.svc.repo.NextQueuedGeneration()
		if err != nil {
			if !d.retryClaim(d.wakeC, err) {
				return
			}
			continue
		}
		if gen == nil {
			select {
			case <-d.done:
				return
			case <-d.wakeC: // re-check the queue
			}
			continue
		}
		// Claimed. Run to completion (or cooperative cancel). A canceled-queued
		// session has already transitioned to canceled, so the claim above
		// returns nil and the slot refills without running anything.
		d.execute(gen)
	}
}

// claimRetryDelay spaces out the retries of a claim that failed.
const claimRetryDelay = 50 * time.Millisecond

// retryClaim pauses after a claim that failed, and reports whether the worker
// should try again. Both claim paths read to find the session and then write to
// take it, so a writer committing in between invalidates the read snapshot and
// the write is refused (SQLITE_BUSY_SNAPSHOT) instead of applied; the next
// attempt starts from a fresh snapshot.
//
// The wake channel alone is not a safe place to wait: the session the claim
// failed on is already in the queue, so no further enqueue is coming to wake
// this worker, and the queue would stall until an unrelated session arrived.
// The timer bounds that wait, and the failure is never silent.
func (d *dispatcher) retryClaim(wake chan struct{}, err error) bool {
	log.Printf("dispatcher: claim failed, retrying in %s: %v", claimRetryDelay, err)
	timer := time.NewTimer(claimRetryDelay)
	defer timer.Stop()
	select {
	case <-d.done:
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

// execute runs one claimed generation with cooperative cancellation. Only the
// successful completion transaction touches the operation's current revision;
// failure/cancel/interruption leave the operation exactly as it was.
func (d *dispatcher) execute(gen *sqlite.PlanGeneration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := parseGenerationRequest(gen.RequestJSON)
	if err != nil {
		d.fail(gen, "DRAFT_LOAD_FAILED", "failed to load the frozen draft")
		return
	}
	members, err := d.svc.repo.ListWorksetMembers(gen.WorksetID)
	if err != nil {
		d.fail(gen, "MEMBERS_LOAD_FAILED", "failed to load members")
		return
	}
	w, err := d.svc.repo.GetWorkset(gen.WorksetID)
	if err != nil {
		d.fail(gen, "WORKSET_LOAD_FAILED", "failed to load workset")
		return
	}

	checkCancel := func() {
		if c, _ := d.svc.repo.GetGeneration(gen.GenerationID); c != nil && c.CancelRequested {
			cancel()
		}
	}

	// Fresh facts first: the inventory is only as current as the last scan, and
	// a plan made from a stale one is the drift the executor refuses later.
	memberFolders, rootsErr := d.svc.participatingRoots(gen.OperationType, req.Draft, members)
	if rootsErr != nil {
		d.fail(gen, "MEMBERS_LOAD_FAILED", "failed to resolve the participating members")
		return
	}
	if scanErr := d.svc.refreshRoots(ctx, memberFolders, w.RootPath, checkCancel); scanErr != nil {
		if ctx.Err() != nil {
			_ = d.svc.repo.CompleteGenerationCanceled(gen.GenerationID)
			return
		}
		d.fail(gen, "SCAN_FAILED", "failed to refresh the scanned inventory before planning")
		return
	}

	// Progress callback updates the session row with root counts and observes
	// the cooperative cancel flag at root boundaries.
	progress := func(p PlanProgress) {
		_ = d.svc.repo.UpdateGenerationProgress(gen.GenerationID, p.CompletedRoots, 0, p.CurrentRoot)
		checkCancel()
	}

	snap, runErr := d.svc.runGeneration(ctx, gen.OperationType, req, members, progress)
	if runErr != nil {
		if ctx.Err() != nil || errors.Is(runErr, context.Canceled) {
			_ = d.svc.repo.CompleteGenerationCanceled(gen.GenerationID)
			return
		}
		code, message := "GENERATION_FAILED", "planning failed"
		if werr, ok := AsError(runErr); ok {
			code, message = werr.Code, werr.Message
		}
		d.fail(gen, code, message)
		return
	}

	steps, roots, components := toRevisionRecords(snap)
	now := time.Now()
	if persistErr := d.svc.repo.PersistOperationRevision(
		gen.GenerationID,
		gen.WorksetID,
		gen.OperationType,
		now,
		sqlite.OperationRevisionPersist{
			PlanID:            "plan-" + genIDNano(gen.GenerationID),
			RootPath:          snap.RootPath,
			SnapshotToken:     "snapshot-" + genIDNano(gen.GenerationID),
			LibraryID:         w.LibraryID,
			DraftHash:         req.DraftHash,
			MemberHash:        req.MemberHash,
			OperationVersion:  gen.ExpectedDraftVersion,
			ExcludedScope:     snap.ExcludedScope,
			DraftSnapshot:     string(req.Draft),
			TaskSchemaVersion: snap.PayloadSchemaVersion,
			Steps:             steps,
			Roots:             roots,
			Components:        components,
		},
	); persistErr != nil {
		d.fail(gen, "PERSIST_FAILED", "failed to persist revision")
		return
	}
}

// reconcileAudioStepType is the frozen step_type value persisted on every
// conversion revision. The step vocabulary retires with the snapshot tables.
const reconcileAudioStepType = "reconcile_audio_outputs"

// toRevisionRecords freezes a task plan snapshot into the repository's
// revision records (the storage adapter's shape).
func toRevisionRecords(snap *PlanSnapshot) (
	steps []sqlite.PlanStepRecord,
	roots []sqlite.PlanRootRecord,
	components []sqlite.PlanComponentRecord,
) {
	steps = []sqlite.PlanStepRecord{{
		StepIndex:           0,
		StepType:            reconcileAudioStepType,
		Status:              snap.Status,
		PolicySchemaVersion: snap.PayloadSchemaVersion,
		PolicyJSON:          string(snap.Payload),
		PolicyHash:          snap.PayloadHash,
		ClassifierTags:      snap.Tags,
		ClassifierHash:      snap.TagsHash,
		StepSummaryJSON:     string(snap.Summary),
	}}
	roots = make([]sqlite.PlanRootRecord, 0, len(snap.Roots))
	for _, r := range snap.Roots {
		roots = append(roots, sqlite.PlanRootRecord{
			RootIndex:            r.Index,
			RootPath:             r.Path,
			RootIdentity:         r.Identity,
			InventoryFingerprint: r.InventoryFingerprint,
			EntryCount:           r.Count,
			RootStatus:           r.Status,
			RootErrorCode:        r.ErrorCode,
			RootErrorMessage:     r.ErrorMessage,
		})
	}
	components = make([]sqlite.PlanComponentRecord, 0, len(snap.Units))
	for _, u := range snap.Units {
		components = append(components, sqlite.PlanComponentRecord{
			StepIndex:      0,
			ComponentIndex: u.Index,
			ComponentID:    u.ID,
			RootIndex:      u.RootIndex,
			Partition:      u.Partition,
			Status:         u.Status,
			ReasonCode:     u.ReasonCode,
			OutcomeJSON:    string(u.Payload),
		})
	}
	return steps, roots, components
}

// fail records a stable system failure on the session row.
func (d *dispatcher) fail(gen *sqlite.PlanGeneration, code, message string) {
	_ = d.svc.repo.MarkGenerationFailed(gen.GenerationID, code, message)
}

// genIDNano produces a sortable plan id from a generation id (nanosecond
// token), keeping plan snapshots collision-free.
func genIDNano(id string) string {
	return strings.TrimPrefix(id, "gen-")
}
