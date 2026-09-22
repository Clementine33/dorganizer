package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/workset"
)

// seedRetentionWorkset persists a current record for plan_generations rows to
// hang off, and returns its id.
func seedRetentionWorkset(t *testing.T, repo *Repository) string {
	t.Helper()
	insertLibrary(t, repo, "lib-1")
	ws, members, op, draft := newOperationFixture(t, "lib-1", "retention")
	if err := repo.ReplaceCurrentWorkset(
		ws,
		members,
		[]workset.Operation{op},
		[]workset.OperationDraft{draft},
		"",
	); err != nil {
		t.Fatalf("seed workset: %v", err)
	}
	return ws.ID
}

// seedFinishedGeneration queues a planning session and moves it to a terminal
// state at the given time: finished_at is both the terminal marker and the
// timestamp retention judges the row by, so it is the only way to make a
// generation row old.
func seedFinishedGeneration(t *testing.T, repo *Repository, worksetID, id string, finishedAt time.Time) {
	t.Helper()
	seedGeneration(t, repo, id, worksetID)
	if _, err := repo.db.Exec(
		"UPDATE plan_generations SET status = 'completed', finished_at = ? WHERE generation_id = ?",
		finishedAt.Format(timeFormat), id,
	); err != nil {
		t.Fatalf("finish seeded generation %s: %v", id, err)
	}
}

func countRows(t *testing.T, repo *Repository, table string) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestRepository_RunRetentionCleanupBatch_DeletesOnlyOlderThanCutoff(t *testing.T) {
	repo := newTestRepository(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := cutoff.Add(-24 * time.Hour) // 2025-05-31
	newTime := cutoff.Add(24 * time.Hour)  // 2025-06-02

	oldScan := &inventory.ScanSession{
		SessionID: "scan-old",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: oldTime,
	}
	if err := repo.CreateScanSession(oldScan); err != nil {
		t.Fatalf("create old scan session: %v", err)
	}

	newScan := &inventory.ScanSession{
		SessionID: "scan-new",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: newTime,
	}
	if err := repo.CreateScanSession(newScan); err != nil {
		t.Fatalf("create new scan session: %v", err)
	}

	stats, err := repo.RunRetentionCleanupBatch(t.Context(), cutoff, cutoff, 0)
	if err != nil {
		t.Fatalf("RunRetentionCleanupBatch: %v", err)
	}
	if stats.DeletedScanSessions != 1 {
		t.Errorf("DeletedScanSessions = %d, want 1", stats.DeletedScanSessions)
	}

	if count := countRows(t, repo, "scan_sessions"); count != 1 {
		t.Errorf("scan_sessions remaining = %d, want 1", count)
	}
	var retainedSession string
	if err = repo.db.QueryRow("SELECT session_id FROM scan_sessions").Scan(&retainedSession); err != nil {
		t.Fatalf("select retained scan session: %v", err)
	}
	if retainedSession != "scan-new" {
		t.Errorf("retained scan session = %q, want scan-new", retainedSession)
	}

	// A batch that deletes nothing is the signal that the tables are clean.
	stats, err = repo.RunRetentionCleanupBatch(t.Context(), cutoff, cutoff, 0)
	if err != nil {
		t.Fatalf("second RunRetentionCleanupBatch: %v", err)
	}
	if stats.DeletedScanSessions != 0 || stats.DeletedGenerations != 0 {
		t.Errorf("second batch deleted %+v, want nothing", stats)
	}
}

// The startup interrupt is what makes a crashed scan row reachable: retention
// deletes terminal rows only, so a row left non-terminal by a dead process
// would otherwise stay forever. Finalizing it also starts its clock: the row
// ages from the moment it was interrupted, never from its long-past
// started_at, so a crash is never deleted the instant it is noticed.
func TestRepository_RunRetentionCleanupBatch_InterruptedScanBecomesEligible(t *testing.T) {
	repo := newTestRepository(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	crashed := &inventory.ScanSession{
		SessionID: "scan-crashed",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "running",
		StartedAt: cutoff.Add(-24 * time.Hour),
	}
	if err := repo.CreateScanSession(crashed); err != nil {
		t.Fatalf("create crashed scan session: %v", err)
	}

	stats, err := repo.RunRetentionCleanupBatch(t.Context(), cutoff, cutoff, 0)
	if err != nil {
		t.Fatalf("RunRetentionCleanupBatch: %v", err)
	}
	if stats.DeletedScanSessions != 0 {
		t.Errorf("DeletedScanSessions = %d, want 0 while the row is non-terminal", stats.DeletedScanSessions)
	}

	interrupted, err := repo.InterruptStaleScanSessions()
	if err != nil {
		t.Fatalf("InterruptStaleScanSessions: %v", err)
	}
	if interrupted != 1 {
		t.Fatalf("interrupted = %d, want 1", interrupted)
	}

	stats, err = repo.RunRetentionCleanupBatch(t.Context(), cutoff, cutoff, 0)
	if err != nil {
		t.Fatalf("RunRetentionCleanupBatch after interrupt: %v", err)
	}
	if stats.DeletedScanSessions != 0 {
		t.Errorf(
			"DeletedScanSessions = %d, want 0: the row ages from the interrupt, not from started_at",
			stats.DeletedScanSessions,
		)
	}

	stats, err = repo.RunRetentionCleanupBatch(t.Context(), time.Now().Add(time.Hour), time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatalf("RunRetentionCleanupBatch past the interrupt: %v", err)
	}
	if stats.DeletedScanSessions != 1 {
		t.Errorf(
			"DeletedScanSessions = %d, want 1 once the interrupted row is past the cutoff",
			stats.DeletedScanSessions,
		)
	}
}

func TestRepository_RunRetentionCleanupBatch_KeepsNonTerminalGenerations(t *testing.T) {
	repo := newTestRepository(t)
	worksetID := seedRetentionWorkset(t, repo)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	seedFinishedGeneration(t, repo, worksetID, "gen-old", cutoff.Add(-24*time.Hour))
	seedFinishedGeneration(t, repo, worksetID, "gen-new", cutoff.Add(24*time.Hour))
	// Queued and running sessions have no finished_at: they are never eligible,
	// however old they are.
	seedGeneration(t, repo, "gen-queued", worksetID)
	seedGeneration(t, repo, "gen-running", worksetID)
	if _, err := repo.db.Exec(
		"UPDATE plan_generations SET status = 'running' WHERE generation_id = 'gen-running'",
	); err != nil {
		t.Fatalf("mark generation running: %v", err)
	}
	// A session that was interrupted at startup has finished_at, so it ages out
	// like any other terminal row.
	seedFinishedGeneration(t, repo, worksetID, "gen-interrupted", cutoff.Add(-48*time.Hour))

	stats, err := repo.RunRetentionCleanupBatch(t.Context(), cutoff, cutoff, 0)
	if err != nil {
		t.Fatalf("RunRetentionCleanupBatch: %v", err)
	}
	if stats.DeletedGenerations != 2 {
		t.Errorf("DeletedGenerations = %d, want 2 (old completed and interrupted)", stats.DeletedGenerations)
	}

	retained := map[string]bool{}
	rows, err := repo.db.Query("SELECT generation_id FROM plan_generations")
	if err != nil {
		t.Fatalf("query retained generations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan generation id: %v", err)
		}
		retained[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}
	for _, want := range []string{"gen-new", "gen-queued", "gen-running"} {
		if !retained[want] {
			t.Errorf("generation %q was deleted, want retained", want)
		}
	}
}

// Each table gets its own bound, so one batch removes at most 2*limit rows and
// a large purge is spread over as many short transactions as it needs.
func TestRepository_RunRetentionCleanupBatch_LimitsEachTable(t *testing.T) {
	repo := newTestRepository(t)
	worksetID := seedRetentionWorkset(t, repo)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour)
	for _, id := range []string{"scan-a", "scan-b", "scan-c"} {
		session := &inventory.ScanSession{
			SessionID: id,
			RootPath:  "/music",
			Kind:      "full",
			Status:    "completed",
			StartedAt: old,
		}
		if err := repo.CreateScanSession(session); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	for _, id := range []string{"gen-a", "gen-b", "gen-c"} {
		seedFinishedGeneration(t, repo, worksetID, id, old)
	}

	for batch, want := range []CleanupStats{
		{DeletedScanSessions: 1, DeletedGenerations: 1},
		{DeletedScanSessions: 1, DeletedGenerations: 1},
		{DeletedScanSessions: 1, DeletedGenerations: 1},
		{},
	} {
		stats, err := repo.RunRetentionCleanupBatch(t.Context(), cutoff, cutoff, 1)
		if err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
		if stats != want {
			t.Fatalf("batch %d = %+v, want %+v", batch, stats, want)
		}
	}
	if count := countRows(t, repo, "scan_sessions"); count != 0 {
		t.Errorf("scan_sessions remaining = %d, want 0", count)
	}
	if count := countRows(t, repo, "plan_generations"); count != 0 {
		t.Errorf("plan_generations remaining = %d, want 0", count)
	}
}
