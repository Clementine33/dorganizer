package scanner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// MockRepository implements Repository interface for testing
type MockRepository struct {
	Sessions           []ScanSession
	StagingEntries     []StagingEntry
	MergeResult        int
	MergeError         error
	CreateErr          error
	UpdateErr          bool
	CapturedStalePaths []string
}

func (m *MockRepository) WriteStagingEntries(sessionID string, entries []StagingEntry) error {
	m.StagingEntries = append(m.StagingEntries, entries...)
	return nil
}

func (m *MockRepository) MergeStaging(sessionID, rootPath string, stalePaths []string) (int, error) {
	m.CapturedStalePaths = stalePaths
	return m.MergeResult, m.MergeError
}

func (m *MockRepository) CreateScanSession(session *ScanSession) error {
	if m.CreateErr != nil {
		return m.CreateErr
	}
	m.Sessions = append(m.Sessions, *session)
	return nil
}

func (m *MockRepository) UpdateScanSessionStatus(sessionID, status, errorCode, errorMessage string) error {
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

// WriteStagingBatch implements pipelineRepo for batch writes (used by pipeline)
func (m *MockRepository) WriteStagingBatch(sessionID string, batch []StagingEntry) error {
	m.StagingEntries = append(m.StagingEntries, batch...)
	return nil
}

// CleanupStagingSession removes staging entries for a session (failure cleanup)
func (m *MockRepository) CleanupStagingSession(sessionID string) error {
	var filtered []StagingEntry
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
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create album1 dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "song.wav"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to write song.wav: %v", err)
	}

	// Create second album directory and file
	if err := os.MkdirAll(filepath.Join(tmp, "album2"), 0755); err != nil {
		t.Fatalf("failed to create album2 dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "album2", "song.mp3"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to write song.mp3: %v", err)
	}

	mock := &MockRepository{
		MergeResult: 2,
	}

	svc := NewScannerService(mock)

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

	// Verify scan enriches format using stdlib extension detection
	var sawWav bool
	for _, e := range mock.StagingEntries {
		if e.Name != "song.wav" {
			continue
		}
		sawWav = true
		if e.Format != "audio/wav" {
			t.Fatalf("expected WAV staging format audio/wav, got %q", e.Format)
		}
	}
	if !sawWav {
		t.Fatal("expected staging entry for song.wav")
	}
}

func TestScannerService_ScanRoot_WithoutRepo(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "album"), 0755)
	os.WriteFile(filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)

	svc := NewScannerService(nil)

	sessionID, err := svc.ScanRoot(tmp)
	if err != nil {
		t.Fatalf("ScanRoot without repo failed: %v", err)
	}

	if sessionID == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestScannerService_ScanRoot_UpdatesSessionOnFailure(t *testing.T) {
	tmp := t.TempDir()

	mock := &MockRepository{
		CreateErr: os.ErrInvalid,
	}

	svc := NewScannerService(mock)

	_, err := svc.ScanRoot(tmp)
	if err == nil {
		t.Error("expected error when CreateScanSession fails")
	}
}

func TestScannerService_ScanFolder_ScopedToSingleFolder(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	os.MkdirAll(album, 0755)
	os.WriteFile(filepath.Join(album, "song.wav"), []byte("dummy"), 0644)

	mock := &MockRepository{
		MergeResult: 1,
	}

	svc := NewScannerService(mock)

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
	os.MkdirAll(album, 0755)
	os.WriteFile(filepath.Join(album, "song1.wav"), []byte("dummy"), 0644)
	os.WriteFile(filepath.Join(album, "song2.mp3"), []byte("dummy"), 0644)

	mock := &MockRepository{
		MergeResult: 2,
		MergeError:  nil,
	}

	svc := NewScannerService(mock)

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

// ========== Task 1: Path Split Tests ==========

// TestScanRoot_UsesRootPath verifies ScanRoot uses parallel directory descent
func TestScanRoot_UsesRootPath(t *testing.T) {
	tmp := t.TempDir()
	subDir := filepath.Join(tmp, "album1")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "song.wav"), []byte("dummy"), 0644)

	mock := &MockRepository{MergeResult: 1}
	svc := NewScannerService(mock)

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

// TestScanFolder_UsesFolderPath verifies ScanFolder uses single-enumerator pattern
func TestScanFolder_UsesFolderPath(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	os.MkdirAll(album, 0755)
	os.WriteFile(filepath.Join(album, "song.wav"), []byte("dummy"), 0644)

	mock := &MockRepository{MergeResult: 1}
	svc := NewScannerService(mock)

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

// ========== Task 2: Root Parallel Walk Tests ==========

// TestWalkRootParallel_RootExcluded verifies root itself is not emitted
func TestWalkRootParallel_RootExcluded(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "album"), 0755)
	os.WriteFile(filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)

	ctx := context.Background()
	var entries []DirEntry
	var mu sync.Mutex

	emit := func(e DirEntry) error {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, e)
		return nil
	}

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, emit)
	if err != nil {
		t.Fatalf("WalkRootEntriesParallel failed: %v", err)
	}

	// Verify root is not in entries
	for _, e := range entries {
		if e.Path == tmp {
			t.Errorf("root path %s should not be in entries", tmp)
		}
	}
}

// TestWalkRootParallel_IncludesFilesAndDirs verifies both files and dirs are emitted
func TestWalkRootParallel_IncludesFilesAndDirs(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "album", "sub"), 0755)
	os.WriteFile(filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)
	os.WriteFile(filepath.Join(tmp, "album", "sub", "nested.mp3"), []byte("dummy"), 0644)

	ctx := context.Background()
	var entries []DirEntry
	var mu sync.Mutex

	emit := func(e DirEntry) error {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, e)
		return nil
	}

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, emit)
	if err != nil {
		t.Fatalf("WalkRootEntriesParallel failed: %v", err)
	}

	// Should have 4 entries: album (dir), sub (dir), song.wav, nested.mp3
	if len(entries) != 4 {
		t.Errorf("expected 4 entries, got %d", len(entries))
	}

	foundFiles := 0
	foundDirs := 0
	foundNames := map[string]bool{}

	for _, e := range entries {
		foundNames[e.Name] = true
		if e.IsDir {
			foundDirs++
		} else {
			foundFiles++
		}
	}

	if foundDirs != 2 {
		t.Errorf("expected 2 directories, got %d", foundDirs)
	}
	if foundFiles != 2 {
		t.Errorf("expected 2 files, got %d", foundFiles)
	}
	expected := []string{"album", "sub", "song.wav", "nested.mp3"}
	for _, name := range expected {
		if !foundNames[name] {
			t.Errorf("expected to find %s", name)
		}
	}
}

// TestWalkRootParallel_DefaultConcurrency verifies default concurrency of 4
func TestWalkRootParallel_DefaultConcurrency(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("dummy"), 0644)

	ctx := context.Background()
	var count int
	var mu sync.Mutex

	emit := func(e DirEntry) error {
		mu.Lock()
		defer mu.Unlock()
		count++
		return nil
	}

	// Test with 0 concurrency (should default to 4)
	err := WalkRootEntriesParallel(ctx, tmp, tmp, 0, emit)
	if err != nil {
		t.Fatalf("WalkRootEntriesParallel with 0 concurrency failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 entry, got %d", count)
	}
}

// TestWalkRootParallel_EmitErrorCancels verifies emit error cancels walk
func TestWalkRootParallel_EmitErrorCancels(t *testing.T) {
	tmp := t.TempDir()
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(tmp, fmt.Sprintf("file%d.txt", i)), []byte("dummy"), 0644)
	}

	ctx := context.Background()
	expectedErr := errors.New("simulated emit error")
	emitCount := 0
	var mu sync.Mutex

	emit := func(e DirEntry) error {
		mu.Lock()
		defer mu.Unlock()
		emitCount++
		if emitCount >= 3 {
			return expectedErr
		}
		return nil
	}

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, emit)
	if err == nil {
		t.Fatal("expected error from emit")
	}
	if !errors.Is(err, expectedErr) && err.Error() != expectedErr.Error() {
		t.Fatalf("expected error %q, got %q", expectedErr, err)
	}
}

// TestWalkRootParallel_ContextCanceledReturnsError verifies cancellation is propagated.
func TestWalkRootParallel_ContextCanceledReturnsError(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, func(DirEntry) error { return nil })
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestWalkRootParallel_HighFanoutSingleWorkerCompletes verifies high-fanout walk does not stall
// when using one worker.
func TestWalkRootParallel_HighFanoutSingleWorkerCompletes(t *testing.T) {
	tmp := t.TempDir()
	for i := 0; i < 12; i++ {
		dir := filepath.Join(tmp, fmt.Sprintf("album-%02d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create test dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "song.wav"), []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err := WalkRootEntriesParallel(ctx, tmp, tmp, 1, func(DirEntry) error { return nil })
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected walk to complete without error, got: %v", err)
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("walk took too long (%v), likely stalled under high fanout", elapsed)
	}
}

// ========== Task 3: Inline Metadata Tests ==========

// TestInlineMetadata_Complete verifies metadata is collected inline
func TestInlineMetadata_Complete(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "album"), 0755)
	os.WriteFile(filepath.Join(tmp, "album", "song.wav"), []byte("dummydat"), 0644)

	ctx := context.Background()
	var entries []DirEntry
	var mu sync.Mutex

	emit := func(e DirEntry) error {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, e)
		return nil
	}

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, emit)
	if err != nil {
		t.Fatalf("WalkRootEntriesParallel failed: %v", err)
	}

	// Verify metadata for song.wav
	for _, e := range entries {
		if e.Name == "song.wav" {
			if e.Size != 8 {
				t.Errorf("expected size 8, got %d", e.Size)
			}
			if e.Mtime == 0 {
				t.Error("expected non-zero mtime")
			}
			if e.IsDir {
				t.Error("expected IsDir=false for song.wav")
			}
		}
		if e.Name == "album" {
			if !e.IsDir {
				t.Error("expected IsDir=true for album")
			}
		}
	}
}

// TestInlineMetadata_InfoErrorPropagation verifies Info() error is propagated
func TestInlineMetadata_InfoErrorPropagation(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "album"), 0755)
	os.WriteFile(filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)

	// Force deterministic Info() failure via seam
	expectedErr := errors.New("simulated Info() failure")
	dirEntryInfoFunc = func(d fs.DirEntry) (fs.FileInfo, error) {
		return nil, expectedErr
	}
	defer func() {
		dirEntryInfoFunc = nil // Reset seam after test
	}()

	ctx := context.Background()
	emit := func(e DirEntry) error { return nil }

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, emit)
	if err == nil {
		t.Fatal("expected error when Info() fails, got nil")
	}
	if !errors.Is(err, expectedErr) && err.Error() != expectedErr.Error() {
		t.Fatalf("expected error %q, got %q", expectedErr, err)
	}
}

// TestInlineMetadata_FolderPath verifies inline metadata for Folder path
func TestInlineMetadata_FolderPath(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	os.MkdirAll(album, 0755)
	os.WriteFile(filepath.Join(album, "song.wav"), []byte("dummydat"), 0644)

	ctx := context.Background()
	var entries []DirEntry
	var mu sync.Mutex

	emit := func(e DirEntry) error {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, e)
		return nil
	}

	err := WalkFolderEntries(ctx, album, tmp, emit)
	if err != nil {
		t.Fatalf("WalkFolderEntries failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Name != "song.wav" {
		t.Errorf("expected song.wav, got %s", e.Name)
	}
	if e.Size != 8 {
		t.Errorf("expected size 8, got %d", e.Size)
	}
	if e.Mtime == 0 {
		t.Error("expected non-zero mtime")
	}
}

// TestWalkFolderEntries_RecursesNestedEntries verifies folder walk includes nested files/dirs
// while keeping single-enumerator streaming behavior.
func TestWalkFolderEntries_RecursesNestedEntries(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	sub := filepath.Join(album, "disc1")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("failed to create nested folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(album, "root.wav"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to write root.wav: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.mp3"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to write nested.mp3: %v", err)
	}

	ctx := context.Background()
	var entries []DirEntry
	var mu sync.Mutex

	err := WalkFolderEntries(ctx, album, tmp, func(e DirEntry) error {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkFolderEntries failed: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
	}

	if !found["disc1"] {
		t.Fatalf("expected nested directory entry disc1, got entries=%v", found)
	}
	if !found["root.wav"] {
		t.Fatalf("expected root-level file root.wav, got entries=%v", found)
	}
	if !found["nested.mp3"] {
		t.Fatalf("expected nested file nested.mp3, got entries=%v", found)
	}
}

// ========== Task 4: Pipeline Tests ==========

// TestPipeline_WalkAndWriterConcurrent verifies walk and writer run concurrently
func TestPipeline_WalkAndWriterConcurrent(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "album"), 0755)
	os.WriteFile(filepath.Join(tmp, "album", "song1.wav"), []byte("dummy1"), 0644)
	os.WriteFile(filepath.Join(tmp, "album", "song2.wav"), []byte("dummy2"), 0644)

	mock := &MockRepository{MergeResult: 2}
	svc := NewScannerService(mock)

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

// TestPipeline_FailureNoMerge verifies failure prevents merge
func TestPipeline_FailureNoMerge(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "song.mp3"), []byte("dummy"), 0644)

	// Repository that fails staging writes
	repo := &RepositoryThatFailsStaging{
		failStagingError: errors.New("simulated staging failure"),
	}
	repo.MergeResult = 42

	svc := NewScannerService(repo)

	_, err := svc.ScanRoot(tmp)
	if err == nil {
		t.Fatal("expected error from ScanRoot")
	}

	// Verify merge was NOT called
	if repo.mergeCalled {
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

// TestPipeline_CleanupOnFailure verifies staging cleanup on failure
func TestPipeline_CleanupOnFailure(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "song.mp3"), []byte("dummy"), 0644)

	repo := &RepositoryThatFailsStaging{
		failStagingError: errors.New("simulated staging failure"),
	}

	svc := NewScannerService(repo)
	_, err := svc.ScanRoot(tmp)
	if err == nil {
		t.Fatal("expected error from ScanRoot")
	}

	// Cleanup should have been called (RepositoryThatFailsStaging has CleanupStagingSession)
	// Any partial staging would be cleaned up on failure
}

// ========== Task 5: Folder Single-Enumerator Tests ==========

// TestScanFolder_SingleEnumerator verifies Folder uses single-enumerator semantics
func TestScanFolder_SingleEnumerator(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	os.MkdirAll(album, 0755)
	os.WriteFile(filepath.Join(album, "song.wav"), []byte("dummy"), 0644)

	mock := &MockRepository{MergeResult: 1}
	svc := NewScannerService(mock)

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

// TestScanFolder_InlineMetadataAndPipeline verifies Folder uses inline metadata + pipeline
func TestScanFolder_InlineMetadataAndPipeline(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	sub := filepath.Join(album, "sub")
	os.MkdirAll(sub, 0755)
	os.WriteFile(filepath.Join(album, "song1.wav"), []byte("dummy1"), 0644)
	os.WriteFile(filepath.Join(album, "song2.wav"), []byte("dummy2"), 0644)

	mock := &MockRepository{MergeResult: 2}
	svc := NewScannerService(mock)

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
