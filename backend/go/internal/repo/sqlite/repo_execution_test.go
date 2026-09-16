package sqlite_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func newExecutionRepo(t *testing.T) *sqlite.Repository {
	t.Helper()
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

const execDraftHash = "draft-hash"

// seedExecutionWorkset writes a workset with a current revision and its draft,
// so the guarded execution insert has real facts to check.
func seedExecutionWorkset(t *testing.T, repo *sqlite.Repository, worksetID, root string) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := repo.DB().Exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES ('lib-1', 'Onsei', ?, ?, ?, ?)
	`, root, root, now, now); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO worksets (id, title, library_id, root_path, root_path_key, version, created_at, updated_at)
		VALUES (?, 'exec ws', 'lib-1', ?, ?, 1, ?, ?)
	`, worksetID, root, root, now, now); err != nil {
		t.Fatalf("seed workset: %v", err)
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO workset_operations (workset_id, operation_type, version, current_revision_id, created_at, updated_at)
		VALUES (?, 'conversion', 2, 'plan-1', ?, ?)
	`, worksetID, now, now); err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO workset_operation_drafts (workset_id, operation_type, schema_version, draft_json, draft_hash, updated_at)
		VALUES (?, 'conversion', 1, '{}', ?, ?)
	`, worksetID, execDraftHash, now); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, snapshot_token, task_kind, task_schema_version, created_at)
		VALUES ('plan-1', ?, 'snap', 'conversion', 1, ?)
	`, root, now); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
}

// insertExecutionRow writes one session row directly.
func insertExecutionRow(t *testing.T, repo *sqlite.Repository, e *sqlite.PlanExecution) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := repo.DB().Exec(`
		INSERT INTO plan_executions (
			execution_id, workset_id, operation_type, plan_id, status,
			idempotency_key, request_hash, expected_operation_version, request_json,
			total_components, total_operations, report_json, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, 'queued', ?, ?, 0, ?, 0, 0, ?, ?, ?)
	`, e.ExecutionID, e.WorksetID, e.OperationType, e.PlanID,
		e.IdempotencyKey, e.RequestHash, e.RequestJSON, e.ReportJSON, now, now); err != nil {
		t.Fatalf("insert execution row %s: %v", e.ExecutionID, err)
	}
}

// createExecution inserts a queued session for the seeded revision.
func createExecution(t *testing.T, repo *sqlite.Repository, e *sqlite.PlanExecution, planID string) error {
	t.Helper()
	return repo.CreateExecutionGuarded(e, sqlite.ExecutionGuards{
		ExpectedOperationVersion: 2,
		ExpectedCurrentRevision:  planID,
		ExpectedDraftHash:        execDraftHash,
	})
}

// newExecution builds a minimal queued session row.
func newExecution(id, worksetID, planID, key string) *sqlite.PlanExecution {
	return &sqlite.PlanExecution{
		ExecutionID:     id,
		WorksetID:       worksetID,
		OperationType:   "conversion",
		PlanID:          planID,
		IdempotencyKey:  key,
		RequestHash:     "hash-" + key,
		RequestJSON:     `{"delete_mode":"soft","units":[]}`,
		ReportJSON:      `[]`,
		TotalComponents: 0,
		CreatedAt:       time.Now(),
	}
}

func TestPlanExecutionClaimCancelAndTerminalGuard(t *testing.T) {
	repo := newExecutionRepo(t)
	seedExecutionWorkset(t, repo, "ws-1", "/music")
	if err := createExecution(t, repo, newExecution("exec-1", "ws-1", "plan-1", "key-1"), "plan-1"); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	claimed, err := repo.NextQueuedExecution()
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	if claimed.Status != sqlite.ExecStatusRunning || claimed.StartedAt.IsZero() {
		t.Fatalf("claimed row = %+v", claimed)
	}
	// The queue is drained: the same claim returns nothing.
	if again, claimErr := repo.NextQueuedExecution(); claimErr != nil || again != nil {
		t.Fatalf("second claim must be empty: %v %+v", claimErr, again)
	}

	// Cancel of a running session sets the cooperative flag only.
	if cancelErr := repo.CancelExecution("exec-1"); cancelErr != nil {
		t.Fatalf("CancelExecution: %v", cancelErr)
	}
	running, err := repo.GetExecution("exec-1")
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if running.Status != sqlite.ExecStatusRunning || !running.CancelRequested {
		t.Fatalf("running row after cancel = %+v", running)
	}

	// Terminal write, then a late worker write must not downgrade it.
	if finishErr := repo.FinishExecution("exec-1", sqlite.ExecStatusSucceeded, "", "", `[]`); finishErr != nil {
		t.Fatalf("FinishExecution: %v", finishErr)
	}
	if finishErr := repo.FinishExecution("exec-1", sqlite.ExecStatusFailed, "X", "late", `[]`); finishErr != nil {
		t.Fatalf("late FinishExecution: %v", finishErr)
	}
	done, err := repo.GetExecution("exec-1")
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if done.Status != sqlite.ExecStatusSucceeded || done.FinishedAt.IsZero() {
		t.Fatalf("terminal row = %+v", done)
	}
	if active, err := repo.GetActiveExecutionForOperation("ws-1", "conversion"); err != nil || active != nil {
		t.Fatalf("terminal session must not be active: %v %+v", err, active)
	}
}

func TestCreateExecutionGuardedRejections(t *testing.T) {
	guards := sqlite.ExecutionGuards{
		ExpectedOperationVersion: 2,
		ExpectedCurrentRevision:  "plan-1",
		ExpectedDraftHash:        execDraftHash,
	}
	t.Run("stale_version", func(t *testing.T) {
		repo := newExecutionRepo(t)
		seedExecutionWorkset(t, repo, "ws-1", "/music")
		stale := guards
		stale.ExpectedOperationVersion = 1
		err := repo.CreateExecutionGuarded(newExecution("exec-1", "ws-1", "plan-1", "key-1"), stale)
		if !errors.Is(err, sqlite.ErrVersionConflict) {
			t.Fatalf("err = %v, want ErrVersionConflict", err)
		}
	})
	t.Run("draft_drift", func(t *testing.T) {
		repo := newExecutionRepo(t)
		seedExecutionWorkset(t, repo, "ws-1", "/music")
		if _, err := repo.DB().Exec(
			"UPDATE workset_operation_drafts SET draft_hash = 'moved-on' WHERE workset_id = 'ws-1'",
		); err != nil {
			t.Fatalf("drift draft: %v", err)
		}
		err := repo.CreateExecutionGuarded(newExecution("exec-1", "ws-1", "plan-1", "key-1"), guards)
		if !errors.Is(err, sqlite.ErrDraftChanged) {
			t.Fatalf("err = %v, want ErrDraftChanged", err)
		}
	})
	t.Run("not_current_revision", func(t *testing.T) {
		repo := newExecutionRepo(t)
		seedExecutionWorkset(t, repo, "ws-1", "/music")
		if _, err := repo.DB().Exec(
			"UPDATE workset_operations SET current_revision_id = 'plan-other' WHERE workset_id = 'ws-1'",
		); err != nil {
			t.Fatalf("promote other revision: %v", err)
		}
		err := repo.CreateExecutionGuarded(newExecution("exec-1", "ws-1", "plan-1", "key-1"), guards)
		if !errors.Is(err, sqlite.ErrRevisionNotFound) {
			t.Fatalf("err = %v, want ErrRevisionNotFound", err)
		}
	})
	t.Run("orphaned_workset", func(t *testing.T) {
		repo := newExecutionRepo(t)
		seedExecutionWorkset(t, repo, "ws-1", "/music")
		if _, err := repo.DB().Exec("UPDATE worksets SET library_id = NULL WHERE id = 'ws-1'"); err != nil {
			t.Fatalf("orphan workset: %v", err)
		}
		err := repo.CreateExecutionGuarded(newExecution("exec-1", "ws-1", "plan-1", "key-1"), guards)
		if !errors.Is(err, sqlite.ErrWorksetOrphaned) {
			t.Fatalf("err = %v, want ErrWorksetOrphaned", err)
		}
	})
	t.Run("generation_in_progress", func(t *testing.T) {
		repo := newExecutionRepo(t)
		seedExecutionWorkset(t, repo, "ws-1", "/music")
		if _, err := repo.DB().Exec(`
			INSERT INTO plan_generations (generation_id, workset_id, operation_type, status, created_at, updated_at)
			VALUES ('gen-1', 'ws-1', 'conversion', 'running', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`); err != nil {
			t.Fatalf("seed generation: %v", err)
		}
		err := repo.CreateExecutionGuarded(newExecution("exec-1", "ws-1", "plan-1", "key-1"), guards)
		if !errors.Is(err, sqlite.ErrGenerationInProgress) {
			t.Fatalf("err = %v, want ErrGenerationInProgress", err)
		}
	})
}

func TestPlanExecutionCancelQueuedSkipsTheWorker(t *testing.T) {
	repo := newExecutionRepo(t)
	seedExecutionWorkset(t, repo, "ws-1", "/music")
	if err := createExecution(t, repo, newExecution("exec-1", "ws-1", "plan-1", "key-1"), "plan-1"); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if cancelErr := repo.CancelExecution("exec-1"); cancelErr != nil {
		t.Fatalf("CancelExecution: %v", cancelErr)
	}
	canceled, err := repo.GetExecution("exec-1")
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if canceled.Status != sqlite.ExecStatusCanceled || canceled.FinishedAt.IsZero() {
		t.Fatalf("canceled row = %+v", canceled)
	}
	if claimed, err := repo.NextQueuedExecution(); err != nil || claimed != nil {
		t.Fatalf("canceled-queued session must never be claimed: %v %+v", err, claimed)
	}
}

func TestPlanExecutionInterruptStaleKeepsPartialReport(t *testing.T) {
	repo := newExecutionRepo(t)
	seedExecutionWorkset(t, repo, "ws-1", "/music")
	queued := newExecution("exec-q", "ws-1", "plan-1", "key-q")
	running := newExecution("exec-r", "ws-1", "plan-2", "key-r")
	running.ReportJSON = `[{"component_index":0,"status":"pending"}]`
	// Rows are written directly: this scenario exercises the startup
	// interruption of leftovers, not the guarded creation path.
	insertExecutionRow(t, repo, queued)
	insertExecutionRow(t, repo, running)
	if _, err := repo.NextQueuedExecution(); err != nil {
		t.Fatalf("claim: %v", err)
	}

	n, err := repo.InterruptStaleExecutions()
	if err != nil {
		t.Fatalf("InterruptStaleExecutions: %v", err)
	}
	if n != 2 {
		t.Fatalf("interrupted = %d, want 2", n)
	}
	for _, id := range []string{"exec-q", "exec-r"} {
		row, loadErr := repo.GetExecution(id)
		if loadErr != nil {
			t.Fatalf("GetExecution %s: %v", id, loadErr)
		}
		if row.Status != sqlite.ExecStatusInterrupted {
			t.Fatalf("%s status = %s, want interrupted", id, row.Status)
		}
	}
	kept, err := repo.GetExecution("exec-r")
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if kept.ReportJSON == "" || kept.ReportJSON == "[]" {
		t.Fatalf("interrupted session lost its partial report: %q", kept.ReportJSON)
	}
}

func TestPlanExecutionIdempotencyKeyIsHeldForTheSessionLife(t *testing.T) {
	repo := newExecutionRepo(t)
	seedExecutionWorkset(t, repo, "ws-1", "/music")
	if err := createExecution(t, repo, newExecution("exec-1", "ws-1", "plan-1", "key-1"), "plan-1"); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	// Same key, same operation: replayed row.
	last := newExecution("exec-2", "ws-1", "plan-1", "key-1")
	last.RequestHash = "hash-key-1"
	if err := createExecution(t, repo, last, "plan-1"); !errors.Is(err, sqlite.ErrExecutionIdemConflict) {
		t.Fatalf("duplicate key err = %v, want ErrExecutionIdemConflict", err)
	}
	existing, err := repo.GetExecutionByOperationKey("ws-1", "conversion", "key-1")
	if err != nil || existing == nil || existing.ExecutionID != "exec-1" {
		t.Fatalf("replay lookup = %v %+v", err, existing)
	}
	if unknown, err := repo.GetExecutionByOperationKey("ws-1", "conversion", "nope"); err != nil || unknown != nil {
		t.Fatalf("unknown key = %v %+v", err, unknown)
	}
}

func TestPlanExecutionOneRowPerRevision(t *testing.T) {
	repo := newExecutionRepo(t)
	seedExecutionWorkset(t, repo, "ws-1", "/music")
	if err := createExecution(t, repo, newExecution("exec-1", "ws-1", "plan-1", "key-1"), "plan-1"); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	// A second session for the same revision is a storage-level conflict, not a
	// check the callers must remember.
	second := newExecution("exec-2", "ws-1", "plan-1", "key-2")
	if err := createExecution(t, repo, second, "plan-1"); !errors.Is(err, sqlite.ErrExecutionIdemConflict) {
		t.Fatalf("second session err = %v, want ErrExecutionIdemConflict", err)
	}
	if existing, err := repo.GetExecutionForRevision(
		"plan-1",
	); err != nil || existing == nil ||
		existing.ExecutionID != "exec-1" {
		t.Fatalf("revision lookup = %v %+v", err, existing)
	}
	if none, err := repo.GetExecutionForRevision("plan-none"); err != nil || none != nil {
		t.Fatalf("unexecuted revision = %v %+v", err, none)
	}
}

func TestPlanExecutionActiveForRootGuard(t *testing.T) {
	repo := newExecutionRepo(t)
	seedExecutionWorkset(t, repo, "ws-1", "/music")
	if err := createExecution(t, repo, newExecution("exec-1", "ws-1", "plan-1", "key-1"), "plan-1"); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	active, err := repo.HasActiveExecutionForRoot("/music")
	if err != nil || !active {
		t.Fatalf("active guard = %v %v, want true", active, err)
	}
	if other, guardErr := repo.HasActiveExecutionForRoot("/elsewhere"); guardErr != nil || other {
		t.Fatalf("unrelated root guard = %v %v, want false", other, guardErr)
	}
	if finishErr := repo.FinishExecution("exec-1", sqlite.ExecStatusSucceeded, "", "", `[]`); finishErr != nil {
		t.Fatalf("FinishExecution: %v", finishErr)
	}
	active, err = repo.HasActiveExecutionForRoot("/music")
	if err != nil || active {
		t.Fatalf("guard after terminal = %v %v, want false", active, err)
	}
}

func TestSyncObservedInventoryLifecycle(t *testing.T) {
	repo := newExecutionRepo(t)
	now := time.Now().Format(time.RFC3339Nano)
	seed := func(path string, size, mtime, contentRev int64, bitrate any) {
		t.Helper()
		if _, err := repo.DB().Exec(`
			INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, content_rev, bitrate, updated_at)
			VALUES (?, '/music', ?, ?, 0, ?, ?, ?, ?, ?)
		`, path, filepath.ToSlash(filepath.Dir(path)), filepath.Base(path), size, mtime, contentRev, bitrate, now); err != nil {
			t.Fatalf("seed entry %s: %v", path, err)
		}
	}
	seed("/music/album/changed.mp3", 100, 1000, 3, 5000)
	seed("/music/album/gone.mp3", 100, 1000, 1, nil)

	err := repo.SyncObservedInventory("/music",
		[]string{"/music/album/gone.mp3"},
		[]sqlite.InventoryFile{
			{Path: "/music/album/changed.mp3", Size: 222, Mtime: 2000},
			{Path: "/music/album/Delete/gone.mp3", Size: 100, Mtime: 1000},
		},
	)
	if err != nil {
		t.Fatalf("SyncObservedInventory: %v", err)
	}

	var contentRev, size, mtime int64
	var bitrate sql.NullInt64
	var dirty int
	if scanErr := repo.DB().QueryRow(`
		SELECT content_rev, size, mtime, bitrate, dirty_flag FROM entries WHERE path = '/music/album/changed.mp3'
	`).Scan(&contentRev, &size, &mtime, &bitrate, &dirty); scanErr != nil {
		t.Fatalf("changed row: %v", scanErr)
	}
	if contentRev != 4 || size != 222 || mtime != 2000 || bitrate.Valid || dirty != 1 {
		t.Fatalf("changed row = rev=%d size=%d mtime=%d bitrate=%v dirty=%d", contentRev, size, mtime, bitrate, dirty)
	}

	var n int
	if countErr := repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries WHERE path = '/music/album/gone.mp3'",
	).Scan(&n); countErr != nil {
		t.Fatalf("removed row: %v", countErr)
	}
	if n != 0 {
		t.Fatal("removed source row must be gone")
	}

	var parent, name string
	var revNew int64
	if scanErr := repo.DB().QueryRow(`
		SELECT parent_path, name, content_rev FROM entries WHERE path = '/music/album/Delete/gone.mp3'
	`).Scan(&parent, &name, &revNew); scanErr != nil {
		t.Fatalf("recovery row: %v", scanErr)
	}
	if parent != "/music/album/Delete" || name != "gone.mp3" || revNew != 1 {
		t.Fatalf("recovery row = parent=%q name=%q rev=%d", parent, name, revNew)
	}

	// An unchanged observation keeps the surviving bitrate fact and revision.
	err = repo.SyncObservedInventory("/music", nil, []sqlite.InventoryFile{
		{Path: "/music/album/changed.mp3", Size: 222, Mtime: 2000},
	})
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if scanErr := repo.DB().QueryRow(`
		SELECT content_rev, dirty_flag FROM entries WHERE path = '/music/album/changed.mp3'
	`).Scan(&contentRev, &dirty); scanErr != nil {
		t.Fatalf("changed row reload: %v", scanErr)
	}
	if contentRev != 4 || dirty != 0 {
		t.Fatalf("unchanged observation must not bump the revision: rev=%d dirty=%d", contentRev, dirty)
	}
}
