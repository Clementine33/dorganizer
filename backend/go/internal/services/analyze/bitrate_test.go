package analyze

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func writeTestMP3Frame(t *testing.T, path string) {
	t.Helper()
	data := append([]byte{0xFF, 0xFB, 0x90, 0x64}, make([]byte, 1024)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("failed to write mp3 %s: %v", path, err)
	}
}

func TestSelectScopedProbeCandidates_OnlyScopedMissingMP3(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/scope/a.mp3", Bitrate: 0},
		{PathPosix: "/scope/b.mp3", Bitrate: 128000},
		{PathPosix: "/scope/c.flac", Bitrate: 0},
		{PathPosix: "/scope/d.MP3", Bitrate: 0},
	}

	idx := selectScopedProbeCandidates(entries)
	if len(idx) != 2 {
		t.Fatalf("expected 2 scoped probe candidates, got %d", len(idx))
	}
	if idx[0] != 0 || idx[1] != 3 {
		t.Fatalf("unexpected candidate indexes: got %v want [0 3]", idx)
	}
}

func TestChunkBitrateUpdates_ChunksAt100(t *testing.T) {
	updates := make([]bitrateUpdate, 0, 250)
	for i := 0; i < 250; i++ {
		updates = append(updates, bitrateUpdate{pathPosix: fmt.Sprintf("/scope/%03d.mp3", i), bitrate: 128000})
	}

	chunks := chunkBitrateUpdates(updates, 100)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 100 || len(chunks[1]) != 100 || len(chunks[2]) != 50 {
		t.Fatalf("unexpected chunk sizes: [%d %d %d]", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestEnrichScopedEntriesBitrate_OnlyPersistsScopedEntries(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-scope-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	const scopedTotal = 120
	scopedEntries := make([]Entry, 0, scopedTotal)

	for i := 0; i < scopedTotal; i++ {
		p := filepath.Join(tmpDir, fmt.Sprintf("in-scope-%03d.mp3", i))
		writeTestMP3Frame(t, p)
		pPosix := filepath.ToSlash(p)
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, NULL)
		`, pPosix, filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert scoped entry: %v", err)
		}
		scopedEntries = append(scopedEntries, Entry{PathPosix: pPosix, Bitrate: 0, Format: "audio/mpeg"})
	}

	outOfScopePath := filepath.Join(tmpDir, "out-of-scope.mp3")
	writeTestMP3Frame(t, outOfScopePath)
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, NULL)
	`, filepath.ToSlash(outOfScopePath), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert out-of-scope entry: %v", err)
	}

	a := NewAnalyzer(repo)
	if err := a.EnrichScopedEntriesBitrate(scopedEntries); err != nil {
		t.Fatalf("expected enrich scoped entries bitrate success, got %v", err)
	}

	var scopedUpdated int
	if err := repo.DB().QueryRow("SELECT COUNT(1) FROM entries WHERE path LIKE ? AND COALESCE(bitrate,0) > 0", filepath.ToSlash(filepath.Join(tmpDir, "in-scope-"))+"%").Scan(&scopedUpdated); err != nil {
		t.Fatalf("failed to count scoped updated bitrates: %v", err)
	}
	if scopedUpdated != scopedTotal {
		t.Fatalf("expected %d scoped bitrates updated, got %d", scopedTotal, scopedUpdated)
	}

	var outOfScopeBitrate int64
	if err := repo.DB().QueryRow("SELECT COALESCE(bitrate, 0) FROM entries WHERE path = ?", filepath.ToSlash(outOfScopePath)).Scan(&outOfScopeBitrate); err != nil {
		t.Fatalf("failed to read out-of-scope bitrate: %v", err)
	}
	if outOfScopeBitrate != 0 {
		t.Fatalf("expected out-of-scope bitrate to remain 0, got %d", outOfScopeBitrate)
	}
}

func TestPersistBitrateUpdates_ReturnsBeginError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-begin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	a := NewAnalyzer(repo)

	repo.Close()

	err = a.persistBitrateUpdates([]bitrateUpdate{{pathPosix: "/scope/a.mp3", bitrate: 128000}}, true)
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

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	if _, err := repo.DB().Exec("DROP TABLE entries"); err != nil {
		t.Fatalf("failed to drop entries table: %v", err)
	}

	a := NewAnalyzer(repo)
	err = a.persistBitrateUpdates([]bitrateUpdate{{pathPosix: "/scope/a.mp3", bitrate: 128000}}, true)
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

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	updates := make([]bitrateUpdate, 0, bitrateUpdateBatchSize+1)
	for i := 0; i < bitrateUpdateBatchSize+1; i++ {
		p := fmt.Sprintf("/scope/%03d.mp3", i)
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, 0)
		`, p, "/scope", 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry %s: %v", p, err)
		}
		updates = append(updates, bitrateUpdate{pathPosix: p, bitrate: 128000})
	}

	failPath := updates[len(updates)-1].pathPosix
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
	if _, err := repo.DB().Exec(triggerSQL); err != nil {
		t.Fatalf("failed to create failure trigger: %v", err)
	}

	a := NewAnalyzer(repo)
	err = a.persistBitrateUpdates(updates, true)
	if err == nil {
		t.Fatal("expected persist error, got nil")
	}
	if !strings.Contains(err.Error(), "forced update failure") {
		t.Fatalf("expected forced update failure error, got %v", err)
	}

	var updatedCount int
	if err := repo.DB().QueryRow("SELECT COUNT(1) FROM entries WHERE COALESCE(bitrate,0) > 0").Scan(&updatedCount); err != nil {
		t.Fatalf("failed to count updated rows after rollback: %v", err)
	}
	if updatedCount != 0 {
		t.Fatalf("expected all updates rolled back, got %d rows with bitrate > 0", updatedCount)
	}

	var firstPathBitrate int64
	if err := repo.DB().QueryRow("SELECT COALESCE(bitrate,0) FROM entries WHERE path = ?", updates[0].pathPosix).Scan(&firstPathBitrate); err != nil {
		t.Fatalf("failed to read first-path bitrate after rollback: %v", err)
	}
	if firstPathBitrate != 0 {
		t.Fatalf("expected earlier-chunk row rollback to 0 bitrate, got %d", firstPathBitrate)
	}
}

func TestEnrichScopedEntriesBitrate_ReturnsPersistError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-enrich-error-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	mp3Path := filepath.Join(tmpDir, "track.mp3")
	writeTestMP3Frame(t, mp3Path)

	if _, err := repo.DB().Exec("DROP TABLE entries"); err != nil {
		t.Fatalf("failed to drop entries table: %v", err)
	}

	a := NewAnalyzer(repo)
	err = a.EnrichScopedEntriesBitrate([]Entry{{PathPosix: filepath.ToSlash(mp3Path), Bitrate: 0, Format: "audio/mpeg"}})
	if err == nil {
		t.Fatal("expected enrich error, got nil")
	}
	if !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("expected no such table error, got %v", err)
	}
}

func TestEnrichScopedEntriesBitrateWithBatchOption_DoesNotEmitGlobalLogMetrics(t *testing.T) {
	var buf bytes.Buffer
	oldOut := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	}()

	a := &Analyzer{}
	err := a.EnrichScopedEntriesBitrateWithBatchOption(
		[]Entry{{PathPosix: "/scope/a.flac", Bitrate: 0, Format: "audio/flac"}},
		true,
	)
	if err != nil {
		t.Fatalf("expected enrich success, got %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Fatalf("expected no global log output, got %q", got)
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

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Insert 200 entries with NULL bitrate.
	const totalEntries = 200
	updates := make([]bitrateUpdate, 0, totalEntries)
	for i := 0; i < totalEntries; i++ {
		p := fmt.Sprintf("/scope/%03d.mp3", i)
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, NULL)
		`, p, "/scope", 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry %s: %v", p, err)
		}
		updates = append(updates, bitrateUpdate{pathPosix: p, bitrate: 128000})
	}

	// Split into 4 groups and persist concurrently using the same repo.
	const numGoroutines = 4
	perGoroutine := totalEntries / numGoroutines

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineIdx int) {
			defer wg.Done()
			start := goroutineIdx * perGoroutine
			end := start + perGoroutine
			if end > len(updates) {
				end = len(updates)
			}
			a := NewAnalyzer(repo)
			if err := a.persistBitrateUpdates(updates[start:end], true); err != nil {
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
	if err := repo.DB().QueryRow("SELECT COUNT(1) FROM entries WHERE COALESCE(bitrate,0) > 0").Scan(&updatedCount); err != nil {
		t.Fatalf("failed to count updated entries: %v", err)
	}
	if updatedCount != totalEntries {
		t.Fatalf("expected %d entries with bitrate > 0, got %d", totalEntries, updatedCount)
	}
}

// mockSQLDB and related types are minimal mocks for unit-testing DB interactions.
// They are not used by the current integration tests but kept for future expansion.

type mockSQLResult struct{}

func (mockSQLResult) LastInsertId() (int64, error) { return 0, nil }
func (mockSQLResult) RowsAffected() (int64, error) { return 0, nil }

type mockSQLTx struct {
	execFn     func(string, ...interface{}) (mockSQLResult, error)
	commitFn   func() error
	rollbackFn func() error
}

func (tx mockSQLTx) Exec(q string, args ...interface{}) (mockSQLResult, error) {
	return tx.execFn(q, args...)
}
func (tx mockSQLTx) Commit() error   { return tx.commitFn() }
func (tx mockSQLTx) Rollback() error { return tx.rollbackFn() }
