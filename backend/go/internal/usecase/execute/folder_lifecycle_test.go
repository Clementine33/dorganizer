package execute

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	exesvc "github.com/onsei/organizer/backend/internal/services/execute"
)

// TestExecuteEventHandler_ItemCompletion_FolderComplete verifies that the usecase
// handler decides folder completion from item-level facts.
func TestExecuteEventHandler_ItemCompletion_FolderComplete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-item-complete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	rootPath := filepath.ToSlash(tmpDir)
	handler := newExecuteEventHandler(&testEventSink{}, nil, rootPath, "plan-1")

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderANorm := attributeFolderPath(rootPath, filepath.Join(folderA, "file0.mp3"))

	handler.lastItemIndexByFolder[folderANorm] = 1

	// Item 0 succeeds — not yet the last item
	handler.OnItemCompleted(0, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderA, "file0.mp3"))})

	// Item 1 succeeds — last item → folder complete
	handler.OnItemCompleted(1, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderA, "file1.mp3"))})

	sink := handler.sink.(*testEventSink)
	completed := folderEvents(sink.events, "folder_completed")
	failed := folderEvents(sink.events, "folder_failed")

	if len(completed) != 1 || !strings.Contains(completed[0], "AlbumA") {
		t.Fatalf("expected 1 folder_completed for AlbumA, got completed=%v failed=%v", completed, failed)
	}
	if len(failed) != 0 {
		t.Fatalf("expected 0 folder_failed, got failed=%v", failed)
	}
}

// TestExecuteEventHandler_ItemCompletion_FolderFailed verifies that the usecase
// handler emits folder_failed when a folder has failures and its last item completes.
func TestExecuteEventHandler_ItemCompletion_FolderFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-item-failed-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	rootPath := filepath.ToSlash(tmpDir)
	handler := newExecuteEventHandler(&testEventSink{}, nil, rootPath, "plan-1")

	folderB := filepath.Join(tmpDir, "AlbumB")
	folderBNorm := attributeFolderPath(rootPath, filepath.Join(folderB, "file.mp3"))

	handler.lastItemIndexByFolder[folderBNorm] = 0

	// Item 0 precondition fails
	handler.OnPreconditionFailed(0, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderB, "file.mp3"))}, sentinelError{msg: "stale"})
	// Item 0 completes
	handler.OnItemCompleted(0, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderB, "file.mp3"))})

	sink := handler.sink.(*testEventSink)
	completed := folderEvents(sink.events, "folder_completed")
	failed := folderEvents(sink.events, "folder_failed")

	if len(completed) != 0 {
		t.Fatalf("expected 0 folder_completed for failed folder, got completed=%v", completed)
	}
	if len(failed) != 1 || !strings.Contains(failed[0], "AlbumB") {
		t.Fatalf("expected 1 folder_failed for AlbumB, got failed=%v events=%+v", failed, sink.events)
	}
}

// TestExecuteEventHandler_ItemCompletion_CrossFolderOrdering verifies that
// folder_completed for a successful folder is emitted before a later folder's error.
func TestExecuteEventHandler_ItemCompletion_CrossFolderOrdering(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-cross-folder-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	rootPath := filepath.ToSlash(tmpDir)
	handler := newExecuteEventHandler(&testEventSink{}, nil, rootPath, "plan-1")

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	folderANorm := attributeFolderPath(rootPath, filepath.Join(folderA, "a.mp3"))
	folderBNorm := attributeFolderPath(rootPath, filepath.Join(folderB, "b.mp3"))

	handler.lastItemIndexByFolder[folderANorm] = 0
	handler.lastItemIndexByFolder[folderBNorm] = 1

	// Item 0 (AlbumA) succeeds → triggers folder_completed for AlbumA
	handler.OnItemCompleted(0, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderA, "a.mp3"))})

	// Item 1 (AlbumB) fails precondition → triggers error + folder_failed for AlbumB
	handler.OnPreconditionFailed(1, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderB, "b.mp3"))}, sentinelError{msg: "stale"})
	handler.OnItemCompleted(1, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderB, "b.mp3"))})

	sink := handler.sink.(*testEventSink)

	folderCompletedIdx := -1
	errorForAlbumBIdx := -1
	for i, e := range sink.events {
		if e.Type == "folder_completed" && e.FolderPath == folderANorm {
			folderCompletedIdx = i
		}
		if e.Type == "error" && e.Code == "EXEC_PRECONDITION_FAILED" && e.FolderPath == folderBNorm {
			errorForAlbumBIdx = i
		}
	}

	if folderCompletedIdx == -1 {
		t.Fatal("expected folder_completed for AlbumA")
	}
	if errorForAlbumBIdx == -1 {
		t.Fatal("expected error for AlbumB")
	}
	if folderCompletedIdx >= errorForAlbumBIdx {
		t.Fatalf("expected folder_completed(%d) before error(%d)", folderCompletedIdx, errorForAlbumBIdx)
	}
}

// TestExecuteEventHandler_ItemCompletion_WithoutPrecompute_Noop verifies that
// without pre-computed folder membership, OnItemCompleted is a no-op.
func TestExecuteEventHandler_ItemCompletion_WithoutPrecompute_Noop(t *testing.T) {
	handler := newExecuteEventHandler(&testEventSink{}, nil, "/root", "plan-1")

	handler.OnItemCompleted(0, exesvc.PlanItem{})

	sink := handler.sink.(*testEventSink)
	if len(sink.events) != 0 {
		t.Fatalf("expected 0 events without precompute, got %+v", sink.events)
	}
}
