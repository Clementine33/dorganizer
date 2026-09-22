package sqlite //nolint:testpackage // white-box tests build a database without the DSN pragmas

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/inventory"
)

// growThenDeleteScanSessions fills the file with scan rows and then deletes them
// through a retention batch, which is how the maintenance pass produces free
// pages in practice.
func growThenDeleteScanSessions(t *testing.T, repo *Repository, rows int) {
	t.Helper()
	ctx := t.Context()
	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	started := cutoff.Add(-24 * time.Hour)
	payload := strings.Repeat("x", 1024)

	for i := range rows {
		session := &inventory.ScanSession{
			SessionID: fmt.Sprintf("scan-%05d", i),
			RootPath:  "/music",
			Kind:      "full",
			Status:    "completed",
			StartedAt: started,
		}
		if err := repo.CreateScanSession(session); err != nil {
			t.Fatalf("create scan %d: %v", i, err)
		}
		if _, err := repo.db.Exec(
			"UPDATE scan_sessions SET error_message = ? WHERE session_id = ?", payload, session.SessionID,
		); err != nil {
			t.Fatalf("pad scan %d: %v", i, err)
		}
	}

	stats, err := repo.RunRetentionCleanupBatch(ctx, cutoff, cutoff, rows*2)
	if err != nil {
		t.Fatalf("RunRetentionCleanupBatch: %v", err)
	}
	if stats.DeletedScanSessions != int64(rows) {
		t.Fatalf("deleted %d rows, want %d", stats.DeletedScanSessions, rows)
	}
}

func dbFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

// A database created before incremental auto-vacuum existed keeps the mode it
// was created with. This also pins the startup path for such a file: opening it
// through NewRepository, whose DSN now asks for incremental auto-vacuum, has to
// keep working rather than fail the whole process.
func TestRepository_AutoVacuumMode_ExistingDatabaseKeepsItsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")

	// Build the file the way an earlier build did: the same schema, no pragmas.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	if err = initSchema(raw); err != nil {
		t.Fatalf("initSchema: %v", err)
	}
	if err = raw.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	repo, err := NewRepository(path)
	if err != nil {
		t.Fatalf("NewRepository on an existing database: %v", err)
	}
	defer repo.Close()

	mode, err := repo.AutoVacuumMode(t.Context())
	if err != nil {
		t.Fatalf("AutoVacuumMode: %v", err)
	}
	if mode != AutoVacuumNone {
		t.Errorf(
			"AutoVacuumMode = %d, want %d: an existing file keeps the mode it was created with",
			mode,
			AutoVacuumNone,
		)
	}
}

func TestRepository_AutoVacuumMode_FreshDatabase(t *testing.T) {
	repo := newTestRepository(t)

	mode, err := repo.AutoVacuumMode(t.Context())
	if err != nil {
		t.Fatalf("AutoVacuumMode: %v", err)
	}
	if mode != AutoVacuumIncremental {
		t.Errorf("AutoVacuumMode = %d, want %d for a freshly created database", mode, AutoVacuumIncremental)
	}
}

// This is the test that catches reclaiming being a silent no-op, which is its
// likeliest failure: it fails if the database is not in incremental auto-vacuum
// mode, if the pragma only steps once per call, if no checkpoint runs, or if
// the file is never truncated.
func TestRepository_ReclaimFreePages_ShrinksTheFileWithoutReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reclaim.db")
	repo, err := NewRepository(path)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	defer repo.Close()
	ctx := t.Context()

	growThenDeleteScanSessions(t, repo, 1500)
	before := dbFileSize(t, path)

	free, err := repo.FreePageCount(ctx)
	if err != nil {
		t.Fatalf("FreePageCount: %v", err)
	}
	if free == 0 {
		t.Fatal("no free pages after deleting every row that grew the file")
	}

	reclaimed, err := repo.ReclaimFreePages(ctx, free)
	if err != nil {
		t.Fatalf("ReclaimFreePages: %v", err)
	}
	if reclaimed == 0 {
		t.Fatalf("reclaimed 0 of %d free pages", free)
	}

	busy, frames, copied, err := repo.CheckpointWAL(ctx)
	if err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
	after := dbFileSize(t, path)
	if after >= before {
		t.Errorf(
			"file is %d bytes, was %d before reclaiming %d pages (wal busy=%d frames=%d copied=%d): "+
				"free pages are not reaching the filesystem",
			after, before, reclaimed, busy, frames, copied,
		)
	}
}

// A reader holds the write-ahead log back, so the file keeps its size until the
// reader leaves. Nothing here waits for it: the pass reports an incomplete
// checkpoint and a later one finishes the job.
func TestRepository_ReclaimFreePages_DefersToAReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader.db")
	repo, err := NewRepository(path)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	defer repo.Close()
	ctx := t.Context()

	growThenDeleteScanSessions(t, repo, 1500)
	before := dbFileSize(t, path)

	reader, err := repo.db.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve reader connection: %v", err)
	}
	readTx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin reader tx: %v", err)
	}
	var counted int
	if err = readTx.QueryRowContext(ctx, "SELECT COUNT(*) FROM scan_sessions").Scan(&counted); err != nil {
		t.Fatalf("reader snapshot: %v", err)
	}

	free, err := repo.FreePageCount(ctx)
	if err != nil {
		t.Fatalf("FreePageCount: %v", err)
	}
	if _, err := repo.ReclaimFreePages(ctx, free); err != nil {
		t.Fatalf("ReclaimFreePages with a reader: %v", err)
	}
	if _, _, _, err := repo.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL with a reader: %v", err)
	}
	if size := dbFileSize(t, path); size < before {
		t.Errorf("file shrank to %d from %d with a reader holding the log", size, before)
	}

	if err := readTx.Rollback(); err != nil {
		t.Fatalf("release reader: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close reader connection: %v", err)
	}

	if _, _, _, err := repo.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL after the reader left: %v", err)
	}
	if after := dbFileSize(t, path); after >= before {
		t.Errorf(
			"file is %d bytes, was %d: the deferred truncation never happened after the reader left",
			after,
			before,
		)
	}
}
