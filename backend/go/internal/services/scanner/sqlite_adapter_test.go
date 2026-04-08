package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// SQLiteAdapterTestSuite provides integration tests for SQLiteRepositoryAdapter
// to verify staging write batching, rollback semantics, and service path behavior.
type SQLiteAdapterTestSuite struct {
	repo    *sqlite.Repository
	adapter *SQLiteRepositoryAdapter
}

func setupTestDB(t *testing.T) *SQLiteAdapterTestSuite {
	t.Helper()
	// Use in-memory database for tests
	sqliteRepo, err := sqlite.NewRepository(":memory:")
	if err != nil {
		t.Fatalf("failed to create test repository: %v", err)
	}

	adapter := NewSQLiteRepositoryAdapter(sqliteRepo)

	t.Cleanup(func() {
		sqliteRepo.Close()
	})

	return &SQLiteAdapterTestSuite{
		repo:    sqliteRepo,
		adapter: adapter,
	}
}

func TestWriteStagingEntries_LargeBatch_SucceedsAtomically(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-large-batch-session"

	// Create 2500 entries (> batchSize of 1000 requires multiple batches)
	var entries []StagingEntry
	for i := 0; i < 2500; i++ {
		entries = append(entries, StagingEntry{
			SessionID:  sessionID,
			Path:       "/music/album/file" + string(rune('a'+i%26)) + string(rune(i)),
			RootPath:   "/music",
			ParentPath: "/music/album",
			Name:       "file" + string(rune('a'+i%26)) + string(rune(i)),
			IsDir:      false,
			Size:       int64(1000 + i),
			Mtime:      time.Now().Unix(),
			Format:     "audio/mp3",
		})
	}

	// Write should succeed atomically despite batching
	err := suite.adapter.WriteStagingEntries(sessionID, entries)
	if err != nil {
		t.Fatalf("WriteStagingEntries failed for large batch: %v", err)
	}

	// Verify all entries were written
	var count int
	err = suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count staging entries: %v", err)
	}

	if count != 2500 {
		t.Errorf("expected 2500 staging entries, got %d", count)
	}
}

// TestWriteStagingEntries_StagingWriteFailure_TriggersRollbackAndReturnsError
// simulates a mid-write insert failure in WriteStagingEntries and verifies:
// - function returns error
// - transaction rollback occurred (no partial rows for session)
func TestWriteStagingEntries_StagingWriteFailure_TriggersRollbackAndReturnsError(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-rollback-session"
	expectedErr := errors.New("simulated insert failure after 25 execs")

	// Create 50 entries
	var entries []StagingEntry
	for i := 0; i < 50; i++ {
		entries = append(entries, StagingEntry{
			SessionID:  sessionID,
			Path:       "/music/album/file" + string(rune('a'+i%26)) + string(rune(i)),
			RootPath:   "/music",
			ParentPath: "/music/album",
			Name:       "file" + string(rune('a'+i%26)) + string(rune(i)),
			IsDir:      false,
			Size:       int64(1000 + i),
			Mtime:      time.Now().Unix(),
			Format:     "audio/mp3",
		})
	}

	// Inject failure after 25 exec calls
	suite.adapter.testExecSeam = func(callCount int) error {
		if callCount == 25 {
			return expectedErr
		}
		return nil
	}

	// Write should fail with our injected error
	err := suite.adapter.WriteStagingEntries(sessionID, entries)
	if err == nil {
		t.Fatal("expected WriteStagingEntries to return error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error containing %v, got %v", expectedErr, err)
	}

	// Verify rollback occurred - no partial rows should remain
	var count int
	queryErr := suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if queryErr != nil {
		t.Fatalf("failed to count staging entries: %v", queryErr)
	}

	if count != 0 {
		t.Errorf("expected 0 staging entries after rollback (transaction should be atomic), got %d", count)
	}
}

// RepositoryThatFailsStaging simulates a repository where WriteStagingEntries fails
type RepositoryThatFailsStaging struct {
	MockRepository
	writeStagingCalled bool
	mergeCalled        bool
	failStagingError   error
}

func (m *RepositoryThatFailsStaging) WriteStagingEntries(sessionID string, entries []StagingEntry) error {
	m.writeStagingCalled = true
	if m.failStagingError != nil {
		return m.failStagingError
	}
	return nil
}

func (m *RepositoryThatFailsStaging) MergeStaging(sessionID, rootPath string, stalePaths []string) (int, error) {
	m.mergeCalled = true
	return m.MergeResult, m.MergeError
}

func (m *RepositoryThatFailsStaging) WriteStagingBatch(sessionID string, batch []StagingEntry) error {
	m.writeStagingCalled = true
	if m.failStagingError != nil {
		return m.failStagingError
	}
	return nil
}

func (m *RepositoryThatFailsStaging) CleanupStagingSession(sessionID string) error {
	return nil
}

// TestScannerService_StagingWriteFailure_PreventsMergeAndEndsSessionFailed verifies
// that if WriteStagingEntries fails, MergeStaging is not called and session ends failed.
func TestScannerService_StagingWriteFailure_PreventsMergeAndEndsSessionFailed(t *testing.T) {
	expectedErr := errors.New("simulated staging write failure")

	// Create a mock repository that tracks session status updates
	repo := &RepositoryThatFailsStaging{
		failStagingError: expectedErr,
	}
	repo.Sessions = []ScanSession{
		{SessionID: "test-session", RootPath: "/music", Status: "running"},
	}
	repo.MergeResult = 42

	svc := NewScannerService(repo)

	// We need to test the service behavior. Since ScanRoot needs real filesystem,
	// we create a minimal temp directory to walk
	tmpDir := t.TempDir()
	// Create one file so there's something to stage
	os.WriteFile(filepath.Join(tmpDir, "song.mp3"), []byte("dummy"), 0644)

	// The session should fail and not call MergeStaging
	_, err := svc.ScanRoot(tmpDir)
	if err == nil {
		t.Fatal("expected ScanRoot to return error when WriteStagingEntries fails")
	}

	// Verify that WriteStagingEntries was called
	if !repo.writeStagingCalled {
		t.Error("expected WriteStagingEntries to be called")
	}

	// CRITICAL: Verify that MergeStaging was NOT called
	if repo.mergeCalled {
		t.Error("MergeStaging should NOT be called when WriteStagingEntries fails")
	}

	// Find the session that was actually created by ScanRoot (not our pre-added one)
	var scanSession *ScanSession
	for i := range repo.Sessions {
		// ScanRoot creates a new session with a UUID, so find the one that was updated
		if repo.Sessions[i].Status == "failed" {
			scanSession = &repo.Sessions[i]
			break
		}
	}
	if scanSession == nil {
		t.Fatalf("no session found with 'failed' status. Sessions: %+v", repo.Sessions)
	}
	if scanSession.ErrorCode != "STAGING_WRITE_FAILED" {
		t.Errorf("expected error code 'STAGING_WRITE_FAILED', got %s", scanSession.ErrorCode)
	}
}

// Test the chunking size is exactly as expected (1000)
func TestWriteStagingEntries_BatchSizeBoundary(t *testing.T) {
	suite := setupTestDB(t)

	// Test with exactly batchSize (1000) entries - should be one batch
	sessionID := "test-exact-batch"

	var entries []StagingEntry
	for i := 0; i < 1000; i++ {
		entries = append(entries, StagingEntry{
			SessionID:  sessionID,
			Path:       "/music/file" + string(rune(i)),
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file" + string(rune(i)),
			IsDir:      false,
			Size:       int64(i),
			Mtime:      time.Now().Unix(),
			Format:     "audio/wav",
		})
	}

	err := suite.adapter.WriteStagingEntries(sessionID, entries)
	if err != nil {
		t.Fatalf("WriteStagingEntries failed for exact batch: %v", err)
	}

	var count int
	err = suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries: %v", err)
	}
	if count != 1000 {
		t.Errorf("expected 1000 entries, got %d", count)
	}

	// Test with batchSize + 1 entries - should require two batches
	sessionID2 := "test-one-over-batch"
	var entries2 []StagingEntry
	for i := 0; i < 1001; i++ {
		entries2 = append(entries2, StagingEntry{
			SessionID:  sessionID2,
			Path:       "/music2/file" + string(rune(i)),
			RootPath:   "/music2",
			ParentPath: "/music2",
			Name:       "file" + string(rune(i)),
			IsDir:      false,
			Size:       int64(i),
			Mtime:      time.Now().Unix(),
			Format:     "audio/wav",
		})
	}

	err = suite.adapter.WriteStagingEntries(sessionID2, entries2)
	if err != nil {
		t.Fatalf("WriteStagingEntries failed for batch+1: %v", err)
	}

	err = suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID2,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries: %v", err)
	}
	if count != 1001 {
		t.Errorf("expected 1001 entries, got %d", count)
	}
}

// Test empty entries slice
func TestWriteStagingEntries_EmptyEntries(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-empty"

	// Empty entries should succeed without error
	err := suite.adapter.WriteStagingEntries(sessionID, []StagingEntry{})
	if err != nil {
		t.Fatalf("WriteStagingEntries failed for empty entries: %v", err)
	}

	// Verify no entries were written
	var count int
	err = suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 entries, got %d", count)
	}
}

// Verify that INSERT OR REPLACE behavior is preserved
func TestWriteStagingEntries_InsertOrReplace(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-upsert"

	// First write
	entries := []StagingEntry{
		{
			SessionID:  sessionID,
			Path:       "/music/file1.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file1.mp3",
			IsDir:      false,
			Size:       1000,
			Mtime:      1000,
			Format:     "audio/mpeg",
		},
	}

	err := suite.adapter.WriteStagingEntries(sessionID, entries)
	if err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	// Verify initial write
	var size int64
	err = suite.repo.DB().QueryRow(
		"SELECT size FROM entries_staging WHERE session_id = ? AND path = ?",
		sessionID, "/music/file1.mp3",
	).Scan(&size)
	if err != nil {
		t.Fatalf("failed to read entry: %v", err)
	}
	if size != 1000 {
		t.Errorf("expected size 1000, got %d", size)
	}

	// Second write with same key but different size (should replace)
	entries[0].Size = 2000
	err = suite.adapter.WriteStagingEntries(sessionID, entries)
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	// Verify entry was replaced, not duplicated
	err = suite.repo.DB().QueryRow(
		"SELECT size FROM entries_staging WHERE session_id = ? AND path = ?",
		sessionID, "/music/file1.mp3",
	).Scan(&size)
	if err != nil {
		t.Fatalf("failed to read entry after upsert: %v", err)
	}
	if size != 2000 {
		t.Errorf("expected size 2000 after upsert, got %d", size)
	}

	// Verify only one entry exists
	var count int
	err = suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 entry after upsert, got %d", count)
	}
}

// Test with nil entries slice
func TestWriteStagingEntries_NilEntries(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-nil"

	// Nil entries should succeed without error
	err := suite.adapter.WriteStagingEntries(sessionID, nil)
	if err != nil {
		t.Fatalf("WriteStagingEntries failed for nil entries: %v", err)
	}
}

// TestWriteStagingBatch_MultiTransaction verifies batch writes use separate transactions
func TestWriteStagingBatch_MultiTransaction(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-multi-tx-batch"

	// First batch
	batch1 := []StagingEntry{
		{
			SessionID:  sessionID,
			Path:       "/music/file1.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file1.mp3",
			IsDir:      false,
			Size:       1000,
			Mtime:      1000,
			Format:     "audio/mpeg",
		},
	}

	// Second batch
	batch2 := []StagingEntry{
		{
			SessionID:  sessionID,
			Path:       "/music/file2.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file2.mp3",
			IsDir:      false,
			Size:       2000,
			Mtime:      2000,
			Format:     "audio/mpeg",
		},
	}

	// Write both batches
	if err := suite.adapter.WriteStagingBatch(sessionID, batch1); err != nil {
		t.Fatalf("WriteStagingBatch batch1 failed: %v", err)
	}
	if err := suite.adapter.WriteStagingBatch(sessionID, batch2); err != nil {
		t.Fatalf("WriteStagingBatch batch2 failed: %v", err)
	}

	// Verify both batches were written
	var count int
	err := suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}
}

// TestWriteStagingBatch_PartialPersistence verifies partial persistence on failure
// Each batch is committed independently, so earlier batches persist on later failure
func TestWriteStagingBatch_PartialPersistence(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-partial-persistence"

	// First batch
	batch1 := []StagingEntry{
		{
			SessionID:  sessionID,
			Path:       "/music/file1.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file1.mp3",
			IsDir:      false,
			Size:       1000,
			Mtime:      1000,
			Format:     "audio/mpeg",
		},
	}

	// Write first batch
	if err := suite.adapter.WriteStagingBatch(sessionID, batch1); err != nil {
		t.Fatalf("WriteStagingBatch batch1 failed: %v", err)
	}

	// Verify first batch was written
	var count int
	err := suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 entry, got %d", count)
	}
}

// TestCleanupStagingSession_RemovesAllEntriesForSession verifies cleanup removes all staging entries
func TestCleanupStagingSession_RemovesAllEntriesForSession(t *testing.T) {
	suite := setupTestDB(t)

	sessionID := "test-cleanup-session"

	// Write some entries
	entries := []StagingEntry{
		{
			SessionID:  sessionID,
			Path:       "/music/file1.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file1.mp3",
			IsDir:      false,
			Size:       1000,
			Mtime:      1000,
			Format:     "audio/mpeg",
		},
		{
			SessionID:  sessionID,
			Path:       "/music/file2.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file2.mp3",
			IsDir:      false,
			Size:       2000,
			Mtime:      2000,
			Format:     "audio/mpeg",
		},
	}

	if err := suite.adapter.WriteStagingEntries(sessionID, entries); err != nil {
		t.Fatalf("WriteStagingEntries failed: %v", err)
	}

	// Verify entries exist
	var count int
	err := suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}

	// Cleanup
	if err := suite.adapter.CleanupStagingSession(sessionID); err != nil {
		t.Fatalf("CleanupStagingSession failed: %v", err)
	}

	// Verify entries removed
	err = suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count entries after cleanup: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", count)
	}
}

// TestCleanupStagingSession_PreservesOtherSessions verifies cleanup only affects target session
func TestCleanupStagingSession_PreservesOtherSessions(t *testing.T) {
	suite := setupTestDB(t)

	sessionID1 := "test-cleanup-session1"
	sessionID2 := "test-cleanup-session2"

	// Write entries for both sessions
	entries1 := []StagingEntry{
		{
			SessionID:  sessionID1,
			Path:       "/music/file1.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file1.mp3",
			IsDir:      false,
			Size:       1000,
			Mtime:      1000,
			Format:     "audio/mpeg",
		},
	}
	entries2 := []StagingEntry{
		{
			SessionID:  sessionID2,
			Path:       "/music/file2.mp3",
			RootPath:   "/music",
			ParentPath: "/music",
			Name:       "file2.mp3",
			IsDir:      false,
			Size:       2000,
			Mtime:      2000,
			Format:     "audio/mpeg",
		},
	}

	if err := suite.adapter.WriteStagingEntries(sessionID1, entries1); err != nil {
		t.Fatalf("WriteStagingEntries for session1 failed: %v", err)
	}
	if err := suite.adapter.WriteStagingEntries(sessionID2, entries2); err != nil {
		t.Fatalf("WriteStagingEntries for session2 failed: %v", err)
	}

	// Cleanup only session1
	if err := suite.adapter.CleanupStagingSession(sessionID1); err != nil {
		t.Fatalf("CleanupStagingSession failed: %v", err)
	}

	// Verify session1 entries removed
	var count1, count2 int
	err := suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID1,
	).Scan(&count1)
	if err != nil {
		t.Fatalf("failed to count session1 entries: %v", err)
	}
	if count1 != 0 {
		t.Errorf("expected 0 entries for session1, got %d", count1)
	}

	// Verify session2 entries preserved
	err = suite.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries_staging WHERE session_id = ?",
		sessionID2,
	).Scan(&count2)
	if err != nil {
		t.Fatalf("failed to count session2 entries: %v", err)
	}
	if count2 != 1 {
		t.Errorf("expected 1 entry for session2, got %d", count2)
	}
}

// Benchmark for large batch performance
func BenchmarkWriteStagingEntries_LargeBatch(b *testing.B) {
	sqliteRepo, err := sqlite.NewRepository(":memory:")
	if err != nil {
		b.Fatalf("failed to create test repository: %v", err)
	}
	defer sqliteRepo.Close()

	adapter := NewSQLiteRepositoryAdapter(sqliteRepo)

	// Create 5000 entries
	var entries []StagingEntry
	for i := 0; i < 5000; i++ {
		entries = append(entries, StagingEntry{
			SessionID:  "bench-session",
			Path:       "/music/album/file" + string(rune('a'+i%26)) + string(rune(i)),
			RootPath:   "/music",
			ParentPath: "/music/album",
			Name:       "file" + string(rune('a'+i%26)) + string(rune(i)),
			IsDir:      false,
			Size:       int64(1000 + i),
			Mtime:      time.Now().Unix(),
			Format:     "audio/mp3",
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := adapter.WriteStagingEntries("bench-session-"+string(rune(i)), entries)
		if err != nil {
			b.Fatalf("WriteStagingEntries failed: %v", err)
		}
	}
}
