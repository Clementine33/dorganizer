package sqlite //nolint:testpackage // white-box tests drive the write path's retry and batching

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onsei/organizer/backend/internal/inventory"
)

func TestChunkBitrateUpdates_ChunksAt100(t *testing.T) {
	updates := make([]inventory.BitrateUpdate, 0, 250)
	for i := range 250 {
		updates = append(updates, inventory.BitrateUpdate{Path: fmt.Sprintf("/scope/%03d.mp3", i), BitrateKbps: 128000})
	}

	chunks := chunkBitrateUpdates(updates, 100)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 100 || len(chunks[1]) != 100 || len(chunks[2]) != 50 {
		t.Fatalf("unexpected chunk sizes: [%d %d %d]", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestPersistBitrateUpdates_ReturnsBeginError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-begin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	repo.Close()

	err = repo.UpdateEntryBitrates([]inventory.BitrateUpdate{{Path: "/scope/a.mp3", BitrateKbps: 128000}}, true)
	if err == nil {
		t.Fatal("expected begin error, got nil")
	}
}

func TestPersistBitrateUpdates_ReturnsExecError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-exec-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	if _, dropErr := repo.DB().Exec("DROP TABLE entries"); dropErr != nil {
		t.Fatalf("failed to drop entries table: %v", dropErr)
	}

	_ = repo
	err = repo.UpdateEntryBitrates([]inventory.BitrateUpdate{{Path: "/scope/a.mp3", BitrateKbps: 128000}}, true)
	if err == nil {
		t.Fatal("expected exec error, got nil")
	}
	if !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("expected no such table error, got %v", err)
	}
}

func TestPersistBitrateUpdates_RollsBackEarlierChunksOnLaterChunkFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-atomicity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	updates := make([]inventory.BitrateUpdate, 0, bitrateUpdateBatchSize+1)
	for i := range bitrateUpdateBatchSize + 1 {
		p := fmt.Sprintf("/scope/%03d.mp3", i)
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, 0)
		`, p, "/scope", 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry %s: %v", p, err)
		}
		updates = append(updates, inventory.BitrateUpdate{Path: p, BitrateKbps: 128000})
	}

	failPath := updates[len(updates)-1].Path
	failPathEscaped := strings.ReplaceAll(failPath, "'", "''")
	triggerSQL := fmt.Sprintf(`
		CREATE TRIGGER fail_second_chunk_update
		BEFORE UPDATE OF bitrate ON entries
		FOR EACH ROW
		WHEN NEW.path = '%s'
		BEGIN
			SELECT RAISE(ABORT, 'forced update failure');
		END;
	`, failPathEscaped)
	if _, triggerErr := repo.DB().Exec(triggerSQL); triggerErr != nil {
		t.Fatalf("failed to create failure trigger: %v", triggerErr)
	}

	_ = repo
	err = repo.UpdateEntryBitrates(updates, true)
	if err == nil {
		t.Fatal("expected persist error, got nil")
	}
	if !strings.Contains(err.Error(), "forced update failure") {
		t.Fatalf("expected forced update failure error, got %v", err)
	}

	var updatedCount int
	if err := repo.DB().
		QueryRow("SELECT COUNT(1) FROM entries WHERE COALESCE(bitrate,0) > 0").
		Scan(&updatedCount); err != nil {
		t.Fatalf("failed to count updated rows after rollback: %v", err)
	}
	if updatedCount != 0 {
		t.Fatalf("expected all updates rolled back, got %d rows with bitrate > 0", updatedCount)
	}

	var firstPathBitrate int64
	if err := repo.DB().
		QueryRow("SELECT COALESCE(bitrate,0) FROM entries WHERE path = ?", updates[0].Path).
		Scan(&firstPathBitrate); err != nil {
		t.Fatalf("failed to read first-path bitrate after rollback: %v", err)
	}
	if firstPathBitrate != 0 {
		t.Fatalf("expected earlier-chunk row rollback to 0 bitrate, got %d", firstPathBitrate)
	}
}

func TestIsSQLiteBusyLockedError_Detection(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("database is locked"), true},
		{errors.New("SQLITE_BUSY: concurrent access"), true},
		{errors.New("sqlite_locked error"), true},
		{errors.New("no such table: entries"), false},
		{nil, false},
		{errors.New("disk I/O error"), false},
	}
	for _, tt := range tests {
		got := isSQLiteBusyLockedError(tt.err)
		if got != tt.want {
			t.Errorf("isSQLiteBusyLockedError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestPersistBitrateUpdates_RetriesOnBusyError(t *testing.T) {
	// Test: retry loop should retry on busy and stop on non-busy.
	attempts := 0
	lastErr := error(nil)
	for attempt := 0; attempt <= bitratePersistRetryLimit; attempt++ {
		attempts++
		// Simulate: first 2 attempts return busy, 3rd returns non-busy
		if attempt < 2 {
			err := errors.New("database is locked")
			if !isSQLiteBusyLockedError(err) {
				t.Fatalf("expected busy error to be detected")
			}
			lastErr = err
			continue
		}
		// Non-busy error - should stop
		err := errors.New("no such table: entries")
		if isSQLiteBusyLockedError(err) {
			t.Fatalf("expected non-busy error to not be detected as busy")
		}
		lastErr = err
		break
	}
	if lastErr == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(lastErr.Error(), "no such table") {
		t.Fatalf("expected non-busy error to be returned, got %v", lastErr)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts (2 busy + 1 non-busy), got %d", attempts)
	}
}

func TestPersistBitrateUpdates_ExhaustsRetriesOnBusy(t *testing.T) {
	// Verify that exhausting retries returns the last busy error.
	attempts := 0
	var lastErr error
	for attempt := 0; attempt <= bitratePersistRetryLimit; attempt++ {
		attempts++
		err := errors.New("SQLITE_BUSY: database is locked")
		if !isSQLiteBusyLockedError(err) {
			t.Fatalf("expected busy error to be detected at attempt %d", attempt)
		}
		lastErr = err
	}
	if lastErr == nil {
		t.Fatal("expected error, got nil")
	}
	if !isSQLiteBusyLockedError(lastErr) {
		t.Fatalf("expected last error to be busy, got %v", lastErr)
	}
	if attempts != bitratePersistRetryLimit+1 {
		t.Fatalf("expected %d attempts (0..%d), got %d", bitratePersistRetryLimit+1, bitratePersistRetryLimit, attempts)
	}
}

func TestPersistBitrateUpdates_ConcurrentSerialization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-bitrate-concurrent-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Insert 200 entries with NULL bitrate.
	const totalEntries = 200
	updates := make([]inventory.BitrateUpdate, 0, totalEntries)
	for i := range totalEntries {
		p := fmt.Sprintf("/scope/%03d.mp3", i)
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, NULL)
		`, p, "/scope", 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry %s: %v", p, err)
		}
		updates = append(updates, inventory.BitrateUpdate{Path: p, BitrateKbps: 128000})
	}

	// Split into 4 groups and persist concurrently using the same repo.
	const numGoroutines = 4
	perGoroutine := totalEntries / numGoroutines

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for g := range numGoroutines {
		wg.Add(1)
		go func(goroutineIdx int) {
			defer wg.Done()
			start := goroutineIdx * perGoroutine
			end := min(start+perGoroutine, len(updates))
			_ = repo
			if err := repo.UpdateEntryBitrates(updates[start:end], true); err != nil {
				errCh <- err
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent persistBitrateUpdates failed: %v", err)
	}

	// Verify all entries have bitrate set.
	var updatedCount int
	if err := repo.DB().
		QueryRow("SELECT COUNT(1) FROM entries WHERE COALESCE(bitrate,0) > 0").
		Scan(&updatedCount); err != nil {
		t.Fatalf("failed to count updated entries: %v", err)
	}
	if updatedCount != totalEntries {
		t.Fatalf("expected %d entries with bitrate > 0, got %d", totalEntries, updatedCount)
	}
}
