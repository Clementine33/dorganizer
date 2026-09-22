package filesystem //nolint:testpackage // white-box tests exercise the traversal internals

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"io/fs"
	"time"

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

// TestWalkRootParallel_RootExcluded verifies root itself is not emitted.
func TestWalkRootParallel_RootExcluded(t *testing.T) {
	tmp := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmp, "album"), 0755)
	mustWriteFile(t, filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)

	ctx := context.Background()
	var entries []inventory.DirEntry
	var mu sync.Mutex

	emit := func(e inventory.DirEntry) error {
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

// TestWalkRootParallel_IncludesFilesAndDirs verifies both files and dirs are emitted.
// TestWalkRootParallel_IncludesFilesAndDirs verifies both files and dirs are emitted.
func TestWalkRootParallel_IncludesFilesAndDirs(t *testing.T) {
	tmp := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmp, "album", "sub"), 0755)
	mustWriteFile(t, filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)
	mustWriteFile(t, filepath.Join(tmp, "album", "sub", "nested.mp3"), []byte("dummy"), 0644)

	ctx := context.Background()
	var entries []inventory.DirEntry
	var mu sync.Mutex

	emit := func(e inventory.DirEntry) error {
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

// TestWalkRootParallel_DefaultConcurrency verifies default concurrency of 4.
// TestWalkRootParallel_DefaultConcurrency verifies default concurrency of 4.
func TestWalkRootParallel_DefaultConcurrency(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "file.txt"), []byte("dummy"), 0644)

	ctx := context.Background()
	var count int
	var mu sync.Mutex

	emit := func(e inventory.DirEntry) error {
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

// TestWalkRootParallel_EmitErrorCancels verifies emit error cancels walk.
// TestWalkRootParallel_EmitErrorCancels verifies emit error cancels walk.
func TestWalkRootParallel_EmitErrorCancels(t *testing.T) {
	tmp := t.TempDir()
	for i := range 10 {
		mustWriteFile(t, filepath.Join(tmp, fmt.Sprintf("file%d.txt", i)), []byte("dummy"), 0644)
	}

	ctx := context.Background()
	expectedErr := errors.New("simulated emit error")
	emitCount := 0
	var mu sync.Mutex

	emit := func(e inventory.DirEntry) error {
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
// TestWalkRootParallel_ContextCanceledReturnsError verifies cancellation is propagated.
func TestWalkRootParallel_ContextCanceledReturnsError(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "file.txt"), []byte("dummy"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, func(inventory.DirEntry) error { return nil })
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestWalkRootParallel_HighFanoutSingleWorkerCompletes verifies high-fanout walk does not stall
// when using one worker.
// TestWalkRootParallel_HighFanoutSingleWorkerCompletes verifies high-fanout walk does not stall
// when using one worker.
func TestWalkRootParallel_HighFanoutSingleWorkerCompletes(t *testing.T) {
	tmp := t.TempDir()
	for i := range 12 {
		dir := filepath.Join(tmp, fmt.Sprintf("album-%02d", i))
		mustMkdirAll(t, dir)
		mustWriteFile(t, filepath.Join(dir, "song.wav"), []byte("dummy"), 0644)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err := WalkRootEntriesParallel(ctx, tmp, tmp, 1, func(inventory.DirEntry) error { return nil })
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected walk to complete without error, got: %v", err)
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("walk took too long (%v), likely stalled under high fanout", elapsed)
	}
}

// ========== Task 3: Inline Metadata Tests ==========

// TestInlineMetadata_Complete verifies metadata is collected inline.
// TestInlineMetadata_Complete verifies metadata is collected inline.
func TestInlineMetadata_Complete(t *testing.T) {
	tmp := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmp, "album"), 0755)
	mustWriteFile(t, filepath.Join(tmp, "album", "song.wav"), []byte("dummydat"), 0644)

	ctx := context.Background()
	var entries []inventory.DirEntry
	var mu sync.Mutex

	emit := func(e inventory.DirEntry) error {
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

// TestInlineMetadata_InfoErrorPropagation verifies Info() error is propagated.
// TestInlineMetadata_InfoErrorPropagation verifies Info() error is propagated.
func TestInlineMetadata_InfoErrorPropagation(t *testing.T) {
	tmp := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmp, "album"), 0755)
	mustWriteFile(t, filepath.Join(tmp, "album", "song.wav"), []byte("dummy"), 0644)

	// Force deterministic Info() failure via seam
	expectedErr := errors.New("simulated Info() failure")
	dirEntryInfoFunc = func(d fs.DirEntry) (fs.FileInfo, error) {
		return nil, expectedErr
	}
	defer func() {
		dirEntryInfoFunc = nil // Reset seam after test
	}()

	ctx := context.Background()
	emit := func(e inventory.DirEntry) error { return nil }

	err := WalkRootEntriesParallel(ctx, tmp, tmp, 4, emit)
	if err == nil {
		t.Fatal("expected error when Info() fails, got nil")
	}
	if !errors.Is(err, expectedErr) && err.Error() != expectedErr.Error() {
		t.Fatalf("expected error %q, got %q", expectedErr, err)
	}
}

// TestInlineMetadata_FolderPath verifies inline metadata for Folder path.
// TestInlineMetadata_FolderPath verifies inline metadata for Folder path.
func TestInlineMetadata_FolderPath(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	mustMkdirAll(t, album)
	mustWriteFile(t, filepath.Join(album, "song.wav"), []byte("dummydat"), 0644)

	ctx := context.Background()
	var entries []inventory.DirEntry
	var mu sync.Mutex

	emit := func(e inventory.DirEntry) error {
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
// TestWalkFolderEntries_RecursesNestedEntries verifies folder walk includes nested files/dirs
// while keeping single-enumerator streaming behavior.
func TestWalkFolderEntries_RecursesNestedEntries(t *testing.T) {
	tmp := t.TempDir()
	album := filepath.Join(tmp, "album")
	sub := filepath.Join(album, "disc1")
	mustMkdirAll(t, sub)
	mustWriteFile(t, filepath.Join(album, "root.wav"), []byte("dummy"))
	mustWriteFile(t, filepath.Join(sub, "nested.mp3"), []byte("dummy"))

	ctx := context.Background()
	var entries []inventory.DirEntry
	var mu sync.Mutex

	err := WalkFolderEntries(ctx, album, tmp, func(e inventory.DirEntry) error {
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

// TestPipeline_WalkAndWriterConcurrent verifies walk and writer run concurrently.
