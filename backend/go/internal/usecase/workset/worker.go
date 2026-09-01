package workset

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// dispatcher is a singleton global FIFO scheduler plus a fixed worker pool. It
// is not one goroutine per generation: workers claim sessions one at a time
// from the queue table in julianday(created_at), generation_id order.
type dispatcher struct {
	svc     *serviceImpl
	workers int

	wakeC chan struct{}
	done  chan struct{}
	stop  sync.Once
}

func newDispatcher(svc *serviceImpl, workers int) *dispatcher {
	if workers <= 0 {
		workers = 2
	}
	return &dispatcher{
		svc:     svc,
		workers: workers,
		wakeC:   make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// Start launches the worker pool. It is called once at process startup after
// InterruptStaleGenerations, so the queue is always clean.
func (d *dispatcher) Start() {
	for range d.workers {
		go d.run()
	}
}

// wake pokes the pool after a session is enqueued or a queued session is
// canceled, so workers re-check the queue immediately.
func (d *dispatcher) wake() {
	select {
	case d.wakeC <- struct{}{}:
	default:
	}
}

// Stop shuts the pool down. In-flight generations still running when Stop is
// called are allowed to finish; the next process start marks leftovers as
// interrupted.
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
		if err != nil || gen == nil {
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

// execute runs one claimed generation with cooperative cancellation. Only the
// successful completion transaction touches worksets.current_revision_id;
// failure/cancel/interruption leave the workset exactly as it was.
func (d *dispatcher) execute(gen *sqlite.PlanGeneration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worksetID := gen.WorksetID

	w, err := d.svc.repo.GetWorkset(worksetID)
	if err != nil {
		d.fail(gen, "WORKSET_LOAD_FAILED", "failed to load workset")
		return
	}
	draft, err := d.svc.repo.GetWorksetDraft(worksetID)
	if err != nil || draft == nil {
		d.fail(gen, "DRAFT_LOAD_FAILED", "failed to load draft")
		return
	}
	wf, err := parseWorkflowJSON(draft.StepsJSON)
	if err != nil {
		d.fail(gen, "INVALID_WORKFLOW", "stored draft is invalid")
		return
	}
	members, err := d.svc.repo.ListWorksetMembers(worksetID)
	if err != nil {
		d.fail(gen, "MEMBERS_LOAD_FAILED", "failed to load members")
		return
	}
	roots := make([]string, 0, len(members))
	for _, m := range members {
		roots = append(roots, m.FolderPath)
	}

	// Progress callback updates the session row with root counts and observes
	// the cooperative cancel flag at root boundaries.
	progress := func(p planusecase.Progress) {
		_ = d.svc.repo.UpdateGenerationProgress(gen.GenerationID, p.CompletedRoots, 0, p.CurrentRoot)
		if c, _ := d.svc.repo.GetGeneration(gen.GenerationID); c != nil && c.CancelRequested {
			cancel()
		}
	}

	result, err := planusecase.RunWorkflow(ctx, d.svc.repo, d.svc.configDir, &wf, roots, planusecase.RunOptions{
		MarkMissingRoots: true,
		Progress:         progress,
	})
	if err != nil {
		if ctx.Err() != nil || err == context.Canceled {
			_ = d.svc.repo.CompleteGenerationCanceled(gen.GenerationID)
			return
		}
		code, message := "GENERATION_FAILED", "planning failed"
		if werr, ok := AsError(err); ok {
			code, message = werr.Code, werr.Message
		}
		d.fail(gen, code, message)
		return
	}

	now := time.Now()
	if err := d.svc.repo.PersistWorksetRevision(gen.GenerationID, worksetID, now, sqlite.WorksetRevisionPersist{
		PlanID:          "plan-" + genIDNano(gen.GenerationID),
		RootPath:        result.RootPath,
		SnapshotToken:   "snapshot-" + genIDNano(gen.GenerationID),
		LibraryID:       w.LibraryID,
		DraftHash:       draft.DraftHash,
		MemberHash:      hashMembers(members),
		WorksetsVersion: gen.ExpectedDraftVersion,
		Steps:           result.StepRecords,
		Roots:           result.Roots,
		Components:      result.Components,
	}); err != nil {
		d.fail(gen, "PERSIST_FAILED", "failed to persist revision")
		return
	}
}

func (d *dispatcher) fail(gen *sqlite.PlanGeneration, code, message string) {
	_ = d.svc.repo.MarkGenerationFailed(gen.GenerationID, code, message)
}

// genIDNano produces a sortable plan id from a generation id (nanosecond
// token), keeping plan snapshots collision-free.
func genIDNano(id string) string {
	return strings.TrimPrefix(id, "gen-")
}

// collectWorkflowEntries mirrors plan.usecase.collectWorkflowEntries: it loads
// recognized audio entries under a root with the metadata needed for
// fingerprinting, in normalized sorted order. It is duplicated here because
// the workset package needs a stale-check helper with identical semantics and
// the plan package keeps its version unexported.
func collectWorkflowEntries(repo *sqlite.Repository, root string) ([]reconcile.AudioEntry, error) {
	rootPosix := strings.ReplaceAll(root, "\\", "/")
	prefix := strings.TrimSuffix(rootPosix, "/")
	likePrefix := escapeLikeAll(prefix)
	rows, err := repo.DB().Query(`
		SELECT path, COALESCE(size, 0), COALESCE(mtime, 0), COALESCE(bitrate, 0), COALESCE(format, '')
		FROM entries WHERE is_dir = 0 AND (path = ? OR path LIKE ? ESCAPE '\')
	`, rootPosix, likePrefix+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]reconcile.AudioEntry, 0)
	seen := map[string]struct{}{}
	for rows.Next() {
		var e reconcile.AudioEntry
		if err := rows.Scan(&e.PathPosix, &e.Size, &e.Mtime, &e.Bitrate, &e.Format); err != nil {
			return nil, err
		}
		if _, ok := seen[e.PathPosix]; ok {
			continue
		}
		seen[e.PathPosix] = struct{}{}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].PathPosix < entries[j].PathPosix })
	return entries, nil
}

func escapeLikeAll(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
