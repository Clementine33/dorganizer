package execute

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	exesvc "github.com/onsei/organizer/backend/internal/services/execute"
)

// executeEventHandler implements services/execute.EventHandler and bridges
// execution events to the generic usecase EventSink. It also tracks failed
// folders and item completion to own folder lifecycle boundaries.
type executeEventHandler struct {
	sink     EventSink
	repo     *sqlite.Repository
	rootPath string
	planID   string

	failedFolders map[string]bool

	// Folder lifecycle: maps folder path to the last item index in that folder.
	// Set by the usecase before execution starts.
	lastItemIndexByFolder map[string]int
	// completedItems tracks which items have been fully processed (via OnItemCompleted).
	completedItems map[int]bool
}

// newExecuteEventHandler creates a new event handler that bridges
// services/execute.EventHandler to the usecase EventSink.
func newExecuteEventHandler(sink EventSink, repo *sqlite.Repository, rootPath, planID string) *executeEventHandler {
	return &executeEventHandler{
		sink:                  sink,
		repo:                  repo,
		rootPath:              rootPath,
		planID:                planID,
		failedFolders:         make(map[string]bool),
		lastItemIndexByFolder: make(map[string]int),
		completedItems:        make(map[int]bool),
	}
}

// persistError persists an execute error into error_events.
func (h *executeEventHandler) persistError(code, message, folderPath string) {
	if h.repo == nil {
		return
	}
	var pathPtr *string
	if folderPath != "" {
		pathPtr = &folderPath
	}
	_ = h.repo.CreateErrorEvent(&sqlite.ErrorEvent{
		Scope:     "execute",
		RootPath:  h.rootPath,
		Path:      pathPtr,
		Code:      code,
		Message:   message,
		Retryable: false,
	})
}

// emitError sends a usecase error event through the sink and marks the folder as failed.
func (h *executeEventHandler) emitError(code, message, folderPath string, item exesvc.PlanItem) {
	event := Event{
		Type:           "error",
		Stage:          "execute",
		Code:           code,
		Message:        message,
		FolderPath:     folderPath,
		RootPath:       h.rootPath,
		PlanID:         h.planID,
		ItemSourcePath: filepath.ToSlash(item.SourcePath),
		ItemTargetPath: filepath.ToSlash(item.TargetPath),
		EventID:        generateEventID(),
		Timestamp:      time.Now(),
	}
	_ = h.sink.Emit(event)
	h.persistError(code, message, folderPath)
	if folderPath != "" {
		h.failedFolders[folderPath] = true
	}
}

// OnPreconditionFailed handles precondition failure events from the execute service.
func (h *executeEventHandler) OnPreconditionFailed(itemIndex int, item exesvc.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, firstNonEmpty(item.SourcePath, item.PreconditionPath, item.TargetPath))
	itemType := "delete"
	if item.Type == exesvc.ItemTypeConvert {
		itemType = "convert"
	}
	message := fmt.Sprintf("Item %d (%s) precondition failed: %v", itemIndex, itemType, err)
	if strings.HasPrefix(err.Error(), "SOURCE_MISSING:") {
		message = fmt.Sprintf("SOURCE_MISSING: item %d (%s) source missing: %v", itemIndex, itemType, err)
	}
	h.emitError("EXEC_PRECONDITION_FAILED", message, folderPath, item)
}

// OnStage1CopyFailed handles stage 1 copy failure events from the execute service.
func (h *executeEventHandler) OnStage1CopyFailed(itemIndex int, item exesvc.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, item.SourcePath)
	message := fmt.Sprintf("Stage1 copy failed for item %d: %v", itemIndex, err)
	h.emitError("EXEC_STAGE1_COPY_FAILED", message, folderPath, item)
}

// OnStage2EncodeFailed handles stage 2 encode failure events from the execute service.
func (h *executeEventHandler) OnStage2EncodeFailed(itemIndex int, item exesvc.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, item.SourcePath)
	message := fmt.Sprintf("Stage2 encode failed for item %d: %v", itemIndex, err)
	h.emitError("EXEC_STAGE2_ENCODE_FAILED", message, folderPath, item)
}

// OnStage3CommitFailed handles stage 3 commit failure events from the execute service.
func (h *executeEventHandler) OnStage3CommitFailed(itemIndex int, item exesvc.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, firstNonEmpty(item.TargetPath, item.SourcePath))
	message := fmt.Sprintf("Stage3 commit failed for item %d: %v", itemIndex, err)
	h.emitError("EXEC_STAGE3_COMMIT_FAILED", message, folderPath, item)
}

// OnDeleteFailed handles delete failure events from the execute service.
func (h *executeEventHandler) OnDeleteFailed(itemIndex int, item exesvc.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, firstNonEmpty(item.SourcePath, item.PreconditionPath, item.TargetPath))
	message := fmt.Sprintf("Item %d delete failed: %v", itemIndex, err)
	if strings.HasPrefix(err.Error(), "PERMISSION_DENIED:") {
		message = fmt.Sprintf("PERMISSION_DENIED: item %d delete blocked: %v", itemIndex, err)
	} else if strings.HasPrefix(err.Error(), "TARGET_CONFLICT:") {
		message = fmt.Sprintf("TARGET_CONFLICT: item %d target conflict: %v", itemIndex, err)
	}
	h.emitError("EXEC_DELETE_FAILED", message, folderPath, item)
}

// hasAnyFailedFolder returns true if any folder failures were recorded during execution.
func (h *executeEventHandler) hasAnyFailedFolder() bool {
	return len(h.failedFolders) > 0
}

// OnItemCompleted is called by the lower-level service when an item has been fully processed
// (success or failure). The usecase uses this to determine folder lifecycle boundaries:
// when the last item in a folder completes, it emits folder_failed or folder_completed
// based on whether the folder had any tracked failures.
func (h *executeEventHandler) OnItemCompleted(itemIndex int, item exesvc.PlanItem) {
	if len(h.lastItemIndexByFolder) == 0 {
		return
	}

	h.completedItems[itemIndex] = true

	folderPath := attributeFolderPath(h.rootPath, firstNonEmpty(item.SourcePath, item.PreconditionPath, item.TargetPath))
	if folderPath == "" {
		return
	}

	lastIdx, ok := h.lastItemIndexByFolder[folderPath]
	if !ok || itemIndex != lastIdx {
		return
	}

	// Last item in folder processed — usecase decides outcome.
	if h.failedFolders[folderPath] {
		_ = h.sink.Emit(Event{
			Type:       "folder_failed",
			Message:    fmt.Sprintf("Scope %s failed", folderPath),
			Stage:      "execute",
			FolderPath: folderPath,
			RootPath:   h.rootPath,
			PlanID:     h.planID,
			EventID:    generateEventID(),
			Timestamp:  time.Now(),
		})
	} else {
		_ = h.sink.Emit(Event{
			Type:       "folder_completed",
			Message:    fmt.Sprintf("Scope %s completed", folderPath),
			Stage:      "execute",
			FolderPath: folderPath,
			RootPath:   h.rootPath,
			PlanID:     h.planID,
			EventID:    generateEventID(),
			Timestamp:  time.Now(),
		})
	}
}
