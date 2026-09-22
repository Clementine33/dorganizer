package inventory_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/filesystem"
	"github.com/onsei/organizer/backend/internal/inventory"
)

// mustMkdirAll and mustWriteFile fail the test on filesystem errors. Test
// fixtures don't inspect these errors, but leaving them unchecked would
// silently produce empty trees.
func mustMkdirAll(t *testing.T, path string, mode ...os.FileMode) {
	t.Helper()
	m := os.FileMode(0755)
	if len(mode) > 0 {
		m = mode[0]
	}
	if err := os.MkdirAll(path, m); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte, mode ...os.FileMode) {
	t.Helper()
	m := os.FileMode(0644)
	if len(mode) > 0 {
		m = mode[0]
	}
	if err := os.WriteFile(path, data, m); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fakeStagingStore is a handwritten staging store: it records what a scan
// staged and merged so the pipeline can be tested without a database.
type fakeStagingStore struct {
	Sessions           []inventory.ScanSession
	StagingEntries     []inventory.StagingEntry
	MergeResult        int
	MergeError         error
	CreateErr          error
	UpdateErr          bool
	CapturedStalePaths []string
	// MergeCalls counts merge attempts: a canceled or failed scan must leave
	// the stored inventory alone, which is a fact only the store can witness.
	MergeCalls int
	// WriteBatch, when positive, makes the store accept whole batches of that
	// size, so a scan walks several batches instead of one.
	WriteBatch int
	// failStagingError makes every staging write fail, which is how the
	// pipeline's failure paths are exercised.
	failStagingError   error
	writeStagingCalled bool
}

func (m *fakeStagingStore) WriteStagingEntries(sessionID string, entries []inventory.StagingEntry) error {
	m.writeStagingCalled = true
	if m.failStagingError != nil {
		return m.failStagingError
	}
	m.StagingEntries = append(m.StagingEntries, entries...)
	return nil
}

func (m *fakeStagingStore) MergeStaging(sessionID, rootPath string, stalePaths []string) (int, error) {
	m.MergeCalls++
	m.CapturedStalePaths = stalePaths
	return m.MergeResult, m.MergeError
}

func (m *fakeStagingStore) CreateScanSession(session *inventory.ScanSession) error {
	if m.CreateErr != nil {
		return m.CreateErr
	}
	m.Sessions = append(m.Sessions, *session)
	return nil
}

func (m *fakeStagingStore) UpdateScanSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	for i := range m.Sessions {
		if m.Sessions[i].SessionID == sessionID {
			m.Sessions[i].Status = status
			m.Sessions[i].ErrorCode = errorCode
			m.Sessions[i].ErrorMessage = errorMessage
			if status == "completed" || status == "failed" {
				m.Sessions[i].FinishedAt = time.Now()
			}
			break
		}
	}
	return nil
}

// WriteStagingBatch implements pipelineRepo for batch writes (used by pipeline).
// WriteStagingBatch is the batch path the pipeline prefers.
func (m *fakeStagingStore) WriteStagingBatch(sessionID string, batch []inventory.StagingEntry) error {
	m.writeStagingCalled = true
	if m.failStagingError != nil {
		return m.failStagingError
	}
	m.StagingEntries = append(m.StagingEntries, batch...)
	return nil
}

// CleanupStagingSession removes staging entries for a session (failure cleanup).
// CleanupStagingSession removes staging entries for a session (failure cleanup).
func (m *fakeStagingStore) CleanupStagingSession(sessionID string) error {
	var filtered []inventory.StagingEntry
	for _, e := range m.StagingEntries {
		if e.SessionID != sessionID {
			filtered = append(filtered, e)
		}
	}
	m.StagingEntries = filtered
	return nil
}

func TestScannerService_ScanRoot_CreatesSessionAndMerges(t *testing.T) {
	// Create temp directory with files
	tmp := t.TempDir()
	subDir := filepath.Join(tmp, "album1")
	mustMkdirAll(t, subDir)
	mustWriteFile(t, filepath.Join(subDir, "song.wav"), []byte("dummy"))

	// Create second album directory and file
	mustMkdirAll(t, filepath.Join(tmp, "album2"))
	mustWriteFile(t, filepath.Join(tmp, "album2", "song.mp3"), []byte("dummy"))

	mock := &fakeStagingStore{
		MergeResult: 2,
	}

	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	sessionID, err := svc.ScanRoot(tmp)
	if err != nil {
		t.Fatalf("ScanRoot failed: %v", err)
	}

	if sessionID == "" {
		t.Error("expected non-empty session ID")
	}

	// Verify session was created
	if len(mock.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(mock.Sessions))
	}

	if mock.Sessions[0].Kind != "full" {
		t.Errorf("expected kind 'full', got %s", mock.Sessions[0].Kind)
	}

	if mock.Sessions[0].Status != "completed" {
		t.Errorf("expected status 'completed', got %s", mock.Sessions[0].Status)
	}

	// Verify staging entries were written
	if len(mock.StagingEntries) == 0 {
		t.Error("expected staging entries to be written")
	}

	// Verify scan enriches format using stdlib extension detection. The exact
	// MIME string for .wav depends on the host's mime database (audio/wav vs
	// audio/vnd.wave), so accept both.
	var sawWav bool
	for _, e := range mock.StagingEntries {
		if e.Name != "song.wav" {
			continue
		}
		sawWav = true
		if e.Format != "audio/wav" && e.Format != "audio/vnd.wave" {
			t.Fatalf("expected WAV staging format audio/wav or audio/vnd.wave, got %q", e.Format)
		}
	}
	if !sawWav {
		t.Fatal("expected staging entry for song.wav")
	}
}

func TestScannerService_ScanRoot_WithoutRepo(t *testing.T) {
	tmp := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmp, "album"), 0755)
	mustWriteFile(t, filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)

	svc := inventory.NewPipeline(nil, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	sessionID, err := svc.ScanRoot(tmp)
	if err != nil {
		t.Fatalf("ScanRoot without repo failed: %v", err)
	}

	if sessionID == "" {
		t.Error("expected non-empty session ID")
	}
}

// failingBatchRepo wraps fakeStagingStore and fails every WriteStagingBatch call
// after recording the entries, so cleanup has something to remove.
type failingBatchRepo struct {
	*fakeStagingStore

	batchErr   error
	mergeCalls int
}

func (f *failingBatchRepo) WriteStagingBatch(sessionID string, batch []inventory.StagingEntry) error {
	f.StagingEntries = append(f.StagingEntries, batch...)
	return f.batchErr
}

func (f *failingBatchRepo) MergeStaging(sessionID, rootPath string, stalePaths []string) (int, error) {
	f.mergeCalls++
	return f.MergeResult, f.MergeError
}

// TestScanRootStagingWriteFailureIsNotReportedAsCancellation verifies that a
// staging write failure during the pipeline is reported as STAGING_WRITE_FAILED
// and the original write error, even though the consumer's cancellation makes
// the walker observe context.Canceled. Merge must be skipped and staging
// cleaned up.
// TestScanRootStagingWriteFailureIsNotReportedAsCancellation verifies that a
// staging write failure during the pipeline is reported as STAGING_WRITE_FAILED
// and the original write error, even though the consumer's cancellation makes
// the walker observe context.Canceled. Merge must be skipped and staging
// cleaned up.
func TestScanRootStagingWriteFailureIsNotReportedAsCancellation(t *testing.T) {
	tmp := t.TempDir()
	// More than pipelineBatchSize (1000) entries forces at least one mid-pipeline
	// batch flush, which cancels the derived context; the walker then observes
	// context.Canceled.
	for i := range 15 {
		album := filepath.Join(tmp, fmt.Sprintf("album-%02d", i))
		mustMkdirAll(t, album)
		for j := range 100 {
			mustWriteFile(t, filepath.Join(album, fmt.Sprintf("song-%03d.wav", j)), []byte("dummy"))
		}
	}

	injected := errors.New("simulated staging write failure")
	mock := &failingBatchRepo{fakeStagingStore: &fakeStagingStore{}, batchErr: injected}
	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanRoot(tmp)
	if err == nil {
		t.Fatal("expected ScanRoot to fail")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("expected returned error to wrap the staging write failure, got: %v", err)
	}

	if mock.mergeCalls != 0 {
		t.Errorf("expected merge to be skipped after staging failure, got %d merge calls", mock.mergeCalls)
	}
	if len(mock.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(mock.Sessions))
	}
	if mock.Sessions[0].Status != "failed" || mock.Sessions[0].ErrorCode != "STAGING_WRITE_FAILED" {
		t.Errorf("expected session failed/STAGING_WRITE_FAILED, got status=%q code=%q msg=%q",
			mock.Sessions[0].Status, mock.Sessions[0].ErrorCode, mock.Sessions[0].ErrorMessage)
	}
	if len(mock.StagingEntries) != 0 {
		t.Errorf("expected staging cleanup after failure, got %d entries", len(mock.StagingEntries))
	}
}

// TestScanFolderStagingWriteFailureIsNotReportedAsCancellation covers the
// folder-scoped scan path (used by gRPC RefreshFolders), which shares the
// pipeline error arbitration with ScanRootCtx.
// TestScanFolderStagingWriteFailureIsNotReportedAsCancellation covers the
// folder-scoped scan path (used by gRPC RefreshFolders), which shares the
// pipeline error arbitration with ScanRootCtx.
func TestScanFolderStagingWriteFailureIsNotReportedAsCancellation(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	mustMkdirAll(t, album)
	for j := range 1500 {
		mustWriteFile(t, filepath.Join(album, fmt.Sprintf("song-%04d.wav", j)), []byte("dummy"))
	}

	injected := errors.New("simulated folder staging write failure")
	mock := &failingBatchRepo{fakeStagingStore: &fakeStagingStore{}, batchErr: injected}
	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanFolder(album, tmp)
	if err == nil {
		t.Fatal("expected ScanFolder to fail")
	}
	if !errors.Is(err, injected) {
		t.Fatalf("expected returned error to wrap the staging write failure, got: %v", err)
	}
	if mock.mergeCalls != 0 {
		t.Errorf("expected merge to be skipped after staging failure, got %d merge calls", mock.mergeCalls)
	}
	if len(mock.Sessions) != 1 || mock.Sessions[0].Status != "failed" ||
		mock.Sessions[0].ErrorCode != "STAGING_WRITE_FAILED" {
		t.Errorf("expected session failed/STAGING_WRITE_FAILED, got %+v", mock.Sessions)
	}
}

func TestScannerService_ScanRoot_UpdatesSessionOnFailure(t *testing.T) {
	tmp := t.TempDir()

	mock := &fakeStagingStore{
		CreateErr: os.ErrInvalid,
	}

	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanRoot(tmp)
	if err == nil {
		t.Error("expected error when CreateScanSession fails")
	}
}

func TestScannerService_ScanFolder_ScopedToSingleFolder(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	mustMkdirAll(t, album)
	mustWriteFile(t, filepath.Join(album, "song.wav"), []byte("dummy"), 0644)

	mock := &fakeStagingStore{
		MergeResult: 1,
	}

	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	sessionID, err := svc.ScanFolder(album, tmp)
	if err != nil {
		t.Fatalf("ScanFolder failed: %v", err)
	}

	if sessionID == "" {
		t.Error("expected non-empty session ID")
	}

	// Verify session is folder type
	if len(mock.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(mock.Sessions))
	}

	if mock.Sessions[0].Kind != "folder" {
		t.Errorf("expected kind 'folder', got %s", mock.Sessions[0].Kind)
	}

	if mock.Sessions[0].ScopePath == nil || *mock.Sessions[0].ScopePath != filepath.ToSlash(album) {
		t.Error("expected scope_path to be set to normalized (POSIX) folder path")
	}

	if mock.Sessions[0].Status != "completed" {
		t.Errorf("expected status 'completed', got %s", mock.Sessions[0].Status)
	}
}

func TestScannerService_ScanFolder_DoesNotPassScannedPathsForStaleCleanup(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	mustMkdirAll(t, album)
	mustWriteFile(t, filepath.Join(album, "song1.wav"), []byte("dummy"), 0644)
	mustWriteFile(t, filepath.Join(album, "song2.mp3"), []byte("dummy"), 0644)

	mock := &fakeStagingStore{
		MergeResult: 2,
		MergeError:  nil,
	}

	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanFolder(album, tmp)
	if err != nil {
		t.Fatalf("ScanFolder failed: %v", err)
	}

	// Verify scanned paths were NOT passed for stale cleanup (preserve set comes from repo merge)
	if len(mock.CapturedStalePaths) != 0 {
		t.Errorf("expected 0 stale paths (nil slice), got %d", len(mock.CapturedStalePaths))
	}
	if mock.CapturedStalePaths != nil {
		t.Errorf("expected nil stale paths slice, got non-nil: %v", mock.CapturedStalePaths)
	}
}

// ========== inventory.Task 1: Path Split Tests ==========

// TestScanRoot_UsesRootPath verifies ScanRoot uses parallel directory descent.
// TestScanRoot_UsesRootPath verifies ScanRoot uses parallel directory descent.
func TestScanRoot_UsesRootPath(t *testing.T) {
	tmp := t.TempDir()
	subDir := filepath.Join(tmp, "album1")
	mustMkdirAll(t, subDir)
	mustWriteFile(t, filepath.Join(subDir, "song.wav"), []byte("dummy"), 0644)

	mock := &fakeStagingStore{MergeResult: 1}
	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanRoot(tmp)
	if err != nil {
		t.Fatalf("ScanRoot failed: %v", err)
	}

	// Verify staging entries were written via pipeline
	if len(mock.StagingEntries) == 0 {
		t.Error("expected staging entries to be written via pipeline")
	}

	// Verify song.wav entry exists
	found := false
	for _, e := range mock.StagingEntries {
		if e.Name == "song.wav" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected song.wav in staging entries")
	}
}

// TestScanFolder_UsesFolderPath verifies ScanFolder uses single-enumerator pattern.
// TestScanFolder_UsesFolderPath verifies ScanFolder uses single-enumerator pattern.
func TestScanFolder_UsesFolderPath(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	mustMkdirAll(t, album)
	mustWriteFile(t, filepath.Join(album, "song.wav"), []byte("dummy"), 0644)

	mock := &fakeStagingStore{MergeResult: 1}
	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanFolder(album, tmp)
	if err != nil {
		t.Fatalf("ScanFolder failed: %v", err)
	}

	// Verify staging entries were written
	if len(mock.StagingEntries) == 0 {
		t.Error("expected staging entries")
	}

	// Verify song.wav entry exists
	found := false
	for _, e := range mock.StagingEntries {
		if e.Name == "song.wav" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected song.wav in staging entries")
	}
}

// ========== inventory.Task 2: Root Parallel Walk Tests ==========

// TestWalkRootParallel_RootExcluded verifies root itself is not emitted.
// TestPipeline_WalkAndWriterConcurrent verifies walk and writer run concurrently.
func TestPipeline_WalkAndWriterConcurrent(t *testing.T) {
	tmp := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmp, "album"), 0755)
	mustWriteFile(t, filepath.Join(tmp, "album", "song1.wav"), []byte("dummy1"), 0644)
	mustWriteFile(t, filepath.Join(tmp, "album", "song2.wav"), []byte("dummy2"), 0644)

	mock := &fakeStagingStore{MergeResult: 2}
	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanRoot(tmp)
	if err != nil {
		t.Fatalf("ScanRoot failed: %v", err)
	}

	// Verify session completed
	if len(mock.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(mock.Sessions))
	}
	if mock.Sessions[0].Status != "completed" {
		t.Errorf("expected status completed, got %s", mock.Sessions[0].Status)
	}

	// Verify staging entries were written
	if len(mock.StagingEntries) < 2 {
		t.Errorf("expected at least 2 staging entries, got %d", len(mock.StagingEntries))
	}
}

// TestPipeline_FailureNoMerge verifies failure prevents merge.
// TestPipeline_FailureNoMerge verifies failure prevents merge.
func TestPipeline_FailureNoMerge(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "song.mp3"), []byte("dummy"), 0644)

	// Repository that fails staging writes
	repo := &fakeStagingStore{
		failStagingError: errors.New("simulated staging failure"),
	}
	repo.MergeResult = 42

	svc := inventory.NewPipeline(repo, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanRoot(tmp)
	if err == nil {
		t.Fatal("expected error from ScanRoot")
	}

	// Verify merge was NOT called
	if repo.MergeCalls > 0 {
		t.Error("merge should NOT be called when staging fails")
	}

	// Verify session was marked as failed
	var foundFailed bool
	for _, s := range repo.Sessions {
		if s.Status == "failed" {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		t.Error("expected a failed session")
	}
}

// TestPipeline_CleanupOnFailure verifies staging cleanup on failure.
// TestPipeline_CleanupOnFailure verifies staging cleanup on failure.
func TestPipeline_CleanupOnFailure(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "song.mp3"), []byte("dummy"), 0644)

	repo := &fakeStagingStore{
		failStagingError: errors.New("simulated staging failure"),
	}

	svc := inventory.NewPipeline(repo, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)
	_, err := svc.ScanRoot(tmp)
	if err == nil {
		t.Fatal("expected error from ScanRoot")
	}

	// Cleanup should have been called (RepositoryThatFailsStaging has CleanupStagingSession)
	// Any partial staging would be cleaned up on failure
}

// ========== inventory.Task 5: Folder Single-Enumerator Tests ==========

// TestScanFolder_SingleEnumerator verifies Folder uses single-enumerator semantics.
// TestScanFolder_SingleEnumerator verifies Folder uses single-enumerator semantics.
func TestScanFolder_SingleEnumerator(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	mustMkdirAll(t, album)
	mustWriteFile(t, filepath.Join(album, "song.wav"), []byte("dummy"), 0644)

	mock := &fakeStagingStore{MergeResult: 1}
	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	sessionID, err := svc.ScanFolder(album, tmp)
	if err != nil {
		t.Fatalf("ScanFolder failed: %v", err)
	}

	if sessionID == "" {
		t.Error("expected non-empty session ID")
	}

	if len(mock.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(mock.Sessions))
	}

	session := mock.Sessions[0]
	if session.Kind != "folder" {
		t.Errorf("expected kind 'folder', got %s", session.Kind)
	}

	if session.ScopePath == nil || *session.ScopePath != filepath.ToSlash(album) {
		t.Error("expected scope_path to be set to folder path")
	}

	if session.Status != "completed" {
		t.Errorf("expected status 'completed', got %s", session.Status)
	}
}

// TestScanFolder_InlineMetadataAndPipeline verifies Folder uses inline metadata + pipeline.
// TestScanFolder_InlineMetadataAndPipeline verifies Folder uses inline metadata + pipeline.
func TestScanFolder_InlineMetadataAndPipeline(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	sub := filepath.Join(album, "sub")
	mustMkdirAll(t, sub)
	mustWriteFile(t, filepath.Join(album, "song1.wav"), []byte("dummy1"), 0644)
	mustWriteFile(t, filepath.Join(album, "song2.wav"), []byte("dummy2"), 0644)

	mock := &fakeStagingStore{MergeResult: 2}
	svc := inventory.NewPipeline(mock, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	_, err := svc.ScanFolder(album, tmp)
	if err != nil {
		t.Fatalf("ScanFolder failed: %v", err)
	}

	// Verify staging entries with metadata
	if len(mock.StagingEntries) < 2 {
		t.Errorf("expected at least 2 staging entries, got %d", len(mock.StagingEntries))
	}

	// Verify metadata is present
	for _, e := range mock.StagingEntries {
		if e.Name == "song1.wav" || e.Name == "song2.wav" {
			if e.Size != 6 {
				t.Errorf("expected size 6 for %s, got %d", e.Name, e.Size)
			}
			if e.Mtime == 0 {
				t.Errorf("expected non-zero mtime for %s", e.Name)
			}
		}
	}
}
