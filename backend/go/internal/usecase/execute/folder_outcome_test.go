package execute

import (
	"os"
	"path/filepath"
	"testing"

	exesvc "github.com/onsei/organizer/backend/internal/services/execute"
)

// TestExecuteEventHandler_FolderOutcome_UseCaseOwned verifies that the usecase's
// event handler decides folder outcome (folder_completed vs folder_failed)
// from OnItemCompleted with pre-computed folder boundaries.
func TestExecuteEventHandler_FolderOutcome_UseCaseOwned(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-outcome-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	rootPath := filepath.ToSlash(tmpDir)
	handler := newExecuteEventHandler(&testEventSink{}, nil, rootPath, "plan-1")

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	folderANorm := attributeFolderPath(rootPath, filepath.Join(folderA, "file.mp3"))
	folderBNorm := attributeFolderPath(rootPath, filepath.Join(folderB, "file.mp3"))

	// AlbumA item at index 0, AlbumB item at index 1
	handler.lastItemIndexByFolder[folderANorm] = 0
	handler.lastItemIndexByFolder[folderBNorm] = 1

	// AlbumA (item 0) succeeds
	handler.OnItemCompleted(0, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderA, "file.mp3"))})

	// AlbumB (item 1) precondition failed
	handler.failedFolders[folderBNorm] = true
	handler.OnItemCompleted(1, exesvc.PlanItem{Type: exesvc.ItemTypeDelete, SourcePath: filepath.ToSlash(filepath.Join(folderB, "file.mp3"))})

	sink := handler.sink.(*testEventSink)

	completed := folderEvents(sink.events, "folder_completed")
	failed := folderEvents(sink.events, "folder_failed")

	if len(completed) != 1 || !containsFolder(completed, folderANorm) {
		t.Fatalf("expected folder_completed for AlbumA, got completed=%v failed=%v", completed, failed)
	}
	if len(failed) != 1 || !containsFolder(failed, folderBNorm) {
		t.Fatalf("expected folder_failed for AlbumB, got completed=%v failed=%v", completed, failed)
	}
}

// TestExecuteEventHandler_HasAnyFailedFolder verifies the handler exposes
// whether any folder failures were recorded during execution.
func TestExecuteEventHandler_HasAnyFailedFolder(t *testing.T) {
	handler := newExecuteEventHandler(&testEventSink{}, nil, "/root", "plan-1")

	if handler.hasAnyFailedFolder() {
		t.Fatal("expected no failed folders initially")
	}

	handler.failedFolders["AlbumA"] = true
	if !handler.hasAnyFailedFolder() {
		t.Fatal("expected hasAnyFailedFolder=true after marking failure")
	}
}

// TestExecuteEventHandler_NonFailedFolders verifies the helper correctly
// identifies folders that did not fail.
func TestExecuteEventHandler_NonFailedFolders(t *testing.T) {
	handler := newExecuteEventHandler(&testEventSink{}, nil, "/root", "plan-1")

	handler.failedFolders["AlbumB"] = true

	allFolders := []string{"AlbumA", "AlbumB", "AlbumC"}
	succeeded := nonFailedFolders(allFolders, handler.failedFolders)

	if len(succeeded) != 2 {
		t.Fatalf("expected 2 non-failed folders, got %v", succeeded)
	}
	for _, f := range succeeded {
		if f == "AlbumB" {
			t.Fatalf("AlbumB should not be in non-failed set")
		}
	}
}

// TestExecuteEventHandler_UsecaseOwnsFolderOutcome verifies the usecase
// handler emits folder outcome events based on its own failure tracking
// via OnItemCompleted with pre-computed folder membership.
func TestExecuteEventHandler_UsecaseOwnsFolderOutcome(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-usecase-outcome-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	rootPath := filepath.ToSlash(tmpDir)
	handler := newExecuteEventHandler(&testEventSink{}, nil, rootPath, "plan-1")

	folderB := filepath.Join(tmpDir, "AlbumB")
	folderA := filepath.Join(tmpDir, "AlbumA")
	folderBNorm := attributeFolderPath(rootPath, filepath.Join(folderB, "file.mp3"))
	folderANorm := attributeFolderPath(rootPath, filepath.Join(folderA, "file.mp3"))

	// AlbumB at index 0, AlbumA at index 1
	handler.lastItemIndexByFolder[folderBNorm] = 0
	handler.lastItemIndexByFolder[folderANorm] = 1

	// AlbumB: error callback fires
	handler.OnPreconditionFailed(0, exesvc.PlanItem{
		Type:       exesvc.ItemTypeDelete,
		SourcePath: filepath.ToSlash(filepath.Join(folderB, "file.mp3")),
	}, sentinelError{msg: "stale"})
	// AlbumB: item completes → usecase checks failedFolders → folder_failed
	handler.OnItemCompleted(0, exesvc.PlanItem{
		Type:       exesvc.ItemTypeDelete,
		SourcePath: filepath.ToSlash(filepath.Join(folderB, "file.mp3")),
	})

	// AlbumA: item completes (no failures) → folder_completed
	handler.OnItemCompleted(1, exesvc.PlanItem{
		Type:       exesvc.ItemTypeDelete,
		SourcePath: filepath.ToSlash(filepath.Join(folderA, "file.mp3")),
	})

	sink := handler.sink.(*testEventSink)

	completed := folderEvents(sink.events, "folder_completed")
	failed := folderEvents(sink.events, "folder_failed")

	if len(completed) != 1 || !containsFolder(completed, folderANorm) {
		t.Fatalf("expected folder_completed for %s, got completed=%v failed=%v events=%+v", folderANorm, completed, failed, sink.events)
	}
	if len(failed) != 1 || !containsFolder(failed, folderBNorm) {
		t.Fatalf("expected folder_failed for %s, got failed=%v", folderBNorm, failed)
	}
}
