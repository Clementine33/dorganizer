package grpc

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/execute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ExecutePlan executes a saved plan and streams JobEvent progress.
func (s *OnseiServer) ExecutePlan(req *pb.ExecutePlanRequest, stream grpc.ServerStreamingServer[pb.JobEvent]) error {
	if req.PlanId == "" {
		eventID := generateEventID()
		timestamp := time.Now().Format(time.RFC3339Nano)
		_ = stream.Send(&pb.JobEvent{EventType: "error", Message: "plan_id is required", Stage: "execute", Code: "INVALID_PLAN_ID", FolderPath: "", EventId: eventID, Timestamp: timestamp})
		return status.Errorf(codes.InvalidArgument, "plan_id is required")
	}

	planRow, err := s.repo.GetPlan(req.PlanId)
	if err == sql.ErrNoRows {
		eventID := generateEventID()
		timestamp := time.Now().Format(time.RFC3339Nano)
		_ = stream.Send(&pb.JobEvent{EventType: "error", Message: fmt.Sprintf("Plan not found: %s", req.PlanId), Stage: "execute", Code: "PLAN_NOT_FOUND", FolderPath: "", PlanId: req.PlanId, EventId: eventID, Timestamp: timestamp})
		return status.Errorf(codes.NotFound, "plan not found: %s", req.PlanId)
	}
	if err != nil {
		eventID := generateEventID()
		timestamp := time.Now().Format(time.RFC3339Nano)
		_ = stream.Send(&pb.JobEvent{EventType: "error", Message: fmt.Sprintf("Internal error: %v", err), Stage: "execute", Code: "INTERNAL_ERROR", FolderPath: "", PlanId: req.PlanId, EventId: eventID, Timestamp: timestamp})
		return status.Errorf(codes.Internal, "load plan: %v", err)
	}

	itemRows, err := s.repo.ListPlanItems(req.PlanId)
	if err != nil {
		eventID := generateEventID()
		timestamp := time.Now().Format(time.RFC3339Nano)
		_ = stream.Send(&pb.JobEvent{EventType: "error", Message: fmt.Sprintf("Failed to load plan items: %v", err), Stage: "execute", Code: "INTERNAL_ERROR", FolderPath: "", PlanId: req.PlanId, EventId: eventID, Timestamp: timestamp})
		return status.Errorf(codes.Internal, "load plan items: %v", err)
	}

	rootPath := filepath.FromSlash(firstNonEmpty(planRow.ScanRootPath, planRow.RootPath))
	planID := req.PlanId

	execPlan := &execute.Plan{PlanID: planRow.PlanID, RootPath: rootPath, Items: make([]execute.PlanItem, 0, len(itemRows)), SoftDelete: req.GetSoftDelete()}
	for _, item := range itemRows {
		execItem := execute.PlanItem{
			SourcePath:             filepath.FromSlash(item.SourcePath),
			PreconditionPath:       filepath.FromSlash(item.PreconditionPath),
			PreconditionContentRev: item.PreconditionContentRev,
			PreconditionSize:       item.PreconditionSize,
			PreconditionMtime:      item.PreconditionMtime,
		}
		if item.TargetPath != nil {
			execItem.TargetPath = filepath.FromSlash(*item.TargetPath)
		}
		if strings.EqualFold(item.OpType, "delete") {
			execItem.Type = execute.ItemTypeDelete
		} else {
			execItem.Type = execute.ItemTypeConvert
		}
		execPlan.Items = append(execPlan.Items, execItem)
	}

	_ = stream.Send(&pb.JobEvent{EventType: "started", Message: fmt.Sprintf("Executing plan %s", planID), PlanId: planID, RootPath: filepath.ToSlash(rootPath)})

	hasConvertOp := false
	for _, item := range itemRows {
		if strings.EqualFold(item.OpType, "convert_and_delete") {
			hasConvertOp = true
			break
		}
	}

	var toolsConfig execute.ToolsConfig
	if hasConvertOp {
		toolsConfig, err = s.getToolsConfig()
		if err != nil {
			eventID := generateEventID()
			timestamp := time.Now().Format(time.RFC3339Nano)
			_ = stream.Send(&pb.JobEvent{EventType: "error", Message: fmt.Sprintf("Failed to load tools config: %v", err), Stage: "execute", Code: "CONFIG_INVALID", FolderPath: "", PlanId: planID, RootPath: filepath.ToSlash(rootPath), EventId: eventID, Timestamp: timestamp})
			return status.Errorf(codes.Internal, "load tools config: %v", err)
		}
	}

	eventHandler := &executeEventHandler{stream: stream, rootPath: filepath.ToSlash(rootPath), planID: planID, failedFolders: make(map[string]bool), successfulFolders: make(map[string]bool)}
	emitFolderScopeEvents := func() {
		for folderPath := range eventHandler.failedFolders {
			eventID := generateEventID()
			timestamp := time.Now().Format(time.RFC3339Nano)
			_ = stream.Send(&pb.JobEvent{EventType: "folder_failed", Message: fmt.Sprintf("Scope %s failed", folderPath), Stage: "execute", FolderPath: folderPath, RootPath: filepath.ToSlash(rootPath), PlanId: planID, EventId: eventID, Timestamp: timestamp})
		}
	}

	svc := execute.NewExecuteService(newExecuteRepoAdapter(s.repo), toolsConfig)
	execCfg, _ := s.getExecuteConfig()
	svc.SetExecuteConfig(execCfg)
	svc.SetEventHandler(eventHandler)
	result, err := svc.ExecutePlan(execPlan)
	if err != nil {
		emitFolderScopeEvents()
		if result != nil && result.ErrorCode == "CONFIG_INVALID" {
			eventID := generateEventID()
			timestamp := time.Now().Format(time.RFC3339Nano)
			_ = stream.Send(&pb.JobEvent{EventType: "error", Message: firstNonEmpty(result.ErrorMsg, err.Error()), Stage: "execute", Code: "CONFIG_INVALID", FolderPath: "", PlanId: planID, RootPath: filepath.ToSlash(rootPath), EventId: eventID, Timestamp: timestamp})
		}
		return status.Errorf(codes.FailedPrecondition, "execute plan failed: %v", err)
	}

	emitFolderScopeEvents()

	_ = stream.Send(&pb.JobEvent{EventType: "completed", Message: fmt.Sprintf("Execution complete: %s", result.Status), ProgressPercent: 100, PlanId: planID, RootPath: filepath.ToSlash(rootPath)})
	return nil
}

// executeEventHandler implements execute.EventHandler.
type executeEventHandler struct {
	stream            grpc.ServerStreamingServer[pb.JobEvent]
	rootPath          string
	planID            string
	failedFolders     map[string]bool
	successfulFolders map[string]bool
}

func (h *executeEventHandler) OnPreconditionFailed(itemIndex int, item execute.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, firstNonEmpty(item.SourcePath, item.PreconditionPath, item.TargetPath))
	eventID := generateEventID()
	timestamp := time.Now().Format(time.RFC3339Nano)

	itemType := "delete"
	if item.Type == execute.ItemTypeConvert {
		itemType = "convert"
	}
	message := fmt.Sprintf("Item %d (%s) precondition failed: %v", itemIndex, itemType, err)
	if strings.HasPrefix(err.Error(), "SOURCE_MISSING:") {
		message = fmt.Sprintf("SOURCE_MISSING: item %d (%s) source missing: %v", itemIndex, itemType, err)
	}

	_ = h.stream.Send(&pb.JobEvent{EventType: "error", Stage: "execute", Code: "EXEC_PRECONDITION_FAILED", FolderPath: folderPath, RootPath: h.rootPath, PlanId: h.planID, EventId: eventID, Timestamp: timestamp, ItemSourcePath: item.SourcePath, ItemTargetPath: item.TargetPath, Message: message})
	if folderPath != "" {
		h.failedFolders[folderPath] = true
	}
}

func (h *executeEventHandler) OnStage1CopyFailed(itemIndex int, item execute.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, item.SourcePath)
	eventID := generateEventID()
	timestamp := time.Now().Format(time.RFC3339Nano)
	_ = h.stream.Send(&pb.JobEvent{EventType: "error", Stage: "execute", Code: "EXEC_STAGE1_COPY_FAILED", FolderPath: folderPath, RootPath: h.rootPath, PlanId: h.planID, EventId: eventID, Timestamp: timestamp, ItemSourcePath: item.SourcePath, ItemTargetPath: item.TargetPath, Message: fmt.Sprintf("Stage1 copy failed for item %d: %v", itemIndex, err)})
	if folderPath != "" {
		h.failedFolders[folderPath] = true
	}
}

func (h *executeEventHandler) OnStage2EncodeFailed(itemIndex int, item execute.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, item.SourcePath)
	eventID := generateEventID()
	timestamp := time.Now().Format(time.RFC3339Nano)
	_ = h.stream.Send(&pb.JobEvent{EventType: "error", Stage: "execute", Code: "EXEC_STAGE2_ENCODE_FAILED", FolderPath: folderPath, RootPath: h.rootPath, PlanId: h.planID, EventId: eventID, Timestamp: timestamp, ItemSourcePath: item.SourcePath, ItemTargetPath: item.TargetPath, Message: fmt.Sprintf("Stage2 encode failed for item %d: %v", itemIndex, err)})
	if folderPath != "" {
		h.failedFolders[folderPath] = true
	}
}

func (h *executeEventHandler) OnStage3CommitFailed(itemIndex int, item execute.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, firstNonEmpty(item.TargetPath, item.SourcePath))
	eventID := generateEventID()
	timestamp := time.Now().Format(time.RFC3339Nano)
	_ = h.stream.Send(&pb.JobEvent{EventType: "error", Stage: "execute", Code: "EXEC_STAGE3_COMMIT_FAILED", FolderPath: folderPath, RootPath: h.rootPath, PlanId: h.planID, EventId: eventID, Timestamp: timestamp, ItemSourcePath: item.SourcePath, ItemTargetPath: item.TargetPath, Message: fmt.Sprintf("Stage3 commit failed for item %d: %v", itemIndex, err)})
	if folderPath != "" {
		h.failedFolders[folderPath] = true
	}
}

func (h *executeEventHandler) OnDeleteFailed(itemIndex int, item execute.PlanItem, err error) {
	folderPath := attributeFolderPath(h.rootPath, firstNonEmpty(item.SourcePath, item.PreconditionPath, item.TargetPath))
	eventID := generateEventID()
	timestamp := time.Now().Format(time.RFC3339Nano)

	message := fmt.Sprintf("Item %d delete failed: %v", itemIndex, err)
	if strings.HasPrefix(err.Error(), "PERMISSION_DENIED:") {
		message = fmt.Sprintf("PERMISSION_DENIED: item %d delete blocked: %v", itemIndex, err)
	} else if strings.HasPrefix(err.Error(), "TARGET_CONFLICT:") {
		message = fmt.Sprintf("TARGET_CONFLICT: item %d target conflict: %v", itemIndex, err)
	}

	_ = h.stream.Send(&pb.JobEvent{EventType: "error", Stage: "execute", Code: "EXEC_DELETE_FAILED", FolderPath: folderPath, RootPath: h.rootPath, PlanId: h.planID, EventId: eventID, Timestamp: timestamp, ItemSourcePath: item.SourcePath, ItemTargetPath: item.TargetPath, Message: message})
	if folderPath != "" {
		h.failedFolders[folderPath] = true
	}
}

func (h *executeEventHandler) OnFolderCompleted(folderPath string) {
	eventID := generateEventID()
	timestamp := time.Now().Format(time.RFC3339Nano)
	_ = h.stream.Send(&pb.JobEvent{EventType: "folder_completed", Message: fmt.Sprintf("Scope %s completed", folderPath), Stage: "execute", FolderPath: folderPath, RootPath: h.rootPath, PlanId: h.planID, EventId: eventID, Timestamp: timestamp})
}

type executeRepoAdapter struct {
	repo *sqlite.Repository
}

func newExecuteRepoAdapter(repo *sqlite.Repository) *executeRepoAdapter {
	return &executeRepoAdapter{repo: repo}
}

func (a *executeRepoAdapter) CreateExecuteSession(sessionID, planID, rootPath, status string) error {
	return a.repo.CreateExecuteSession(&sqlite.ExecuteSession{SessionID: sessionID, PlanID: planID, RootPath: filepath.ToSlash(rootPath), Status: status, StartedAt: time.Now()})
}

func (a *executeRepoAdapter) UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	return a.repo.UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage)
}

func (a *executeRepoAdapter) GetEntryContentRev(path string) (int, error) {
	var contentRev int
	err := a.repo.DB().QueryRow("SELECT COALESCE(content_rev, 0) FROM entries WHERE path = ?", filepath.ToSlash(path)).Scan(&contentRev)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return contentRev, err
}
