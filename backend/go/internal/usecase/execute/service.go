package execute

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	exesvc "github.com/onsei/organizer/backend/internal/services/execute"
)

// serviceImpl is the concrete execute usecase service.
type serviceImpl struct {
	repo      *sqlite.Repository
	configDir string
}

// Execute orchestrates the full execution of a persisted plan, streaming events
// through the provided EventSink. It owns plan loading, config interpretation,
// tool execution, folder failure/completion semantics, and error-event persistence.
func (s *serviceImpl) Execute(_ context.Context, req Request, sink EventSink) (Result, error) {
	// 1. Validate PlanID
	if req.PlanID == "" {
		_ = sink.Emit(newEvent("error", "execute", "INVALID_PLAN_ID", "plan_id is required"))
		s.persistExecuteErrorGlobal("INVALID_PLAN_ID", "plan_id is required")
		return Result{}, NewError(ErrKindInvalidArgument, "INVALID_PLAN_ID", "plan_id is required", nil)
	}

	// 2. Load persisted plan
	execPlan, rootPath, err := loadPlan(s.repo, req.PlanID, req.SoftDelete)
	if err != nil {
		useCaseErr := s.mapLoadError(req.PlanID, err, sink)
		return Result{}, useCaseErr
	}

	planID := req.PlanID
	slashRootPath := filepath.ToSlash(rootPath)

	// 3. Emit started event
	if err := sink.Emit(Event{
		Type:      "started",
		Message:   fmt.Sprintf("Executing plan %s", planID),
		PlanID:    planID,
		RootPath:  slashRootPath,
		EventID:   generateEventID(),
		Timestamp: time.Now(),
	}); err != nil {
		return Result{}, err
	}

	// 4. Load config
	hasConvertOp := hasConvertOp(execPlan)
	var toolsConfig exesvc.ToolsConfig
	if hasConvertOp {
		toolsConfig, err = getToolsConfig(s.configDir)
		if err != nil {
			_ = sink.Emit(newEvent("error", "execute", "CONFIG_INVALID",
				fmt.Sprintf("Failed to load tools config: %v", err)))
			s.persistExecuteErrorGlobal("CONFIG_INVALID", fmt.Sprintf("Failed to load tools config: %v", err))
			return Result{}, NewError(ErrKindInternal, "CONFIG_INVALID",
				fmt.Sprintf("load tools config: %v", err), err)
		}
	}

	execCfg, cfgErr := getExecuteConfig(s.configDir)
	if cfgErr != nil {
		// Config is malformed but fallback defaults are safe.
		// Surface the warning through the event sink so the client can alert the user.
		_ = sink.Emit(newEvent("error", "execute", "CONFIG_PARSE_WARNING",
			fmt.Sprintf("Execute config parse error (using defaults): %v", cfgErr)))
	}

	// 5. Create internal event handler wrapper
	handler := newExecuteEventHandler(sink, s.repo, slashRootPath, planID)

	// Pre-compute folder membership so the handler can determine folder lifecycle boundaries.
	// The lower-level service reports only item-level facts; the usecase owns folder outcome.
	if execPlan.RootPath != "" {
		for i, item := range execPlan.Items {
			folder := attributeFolderPath(slashRootPath, firstNonEmpty(item.SourcePath, item.PreconditionPath, item.TargetPath))
			if folder != "" {
				handler.lastItemIndexByFolder[folder] = i
			}
		}
	}

	// 6. Create and configure lower-level execute service
	svc := exesvc.NewExecuteService(newExecuteRepoAdapter(s.repo), toolsConfig)
	svc.SetExecuteConfig(execCfg)
	svc.SetEventHandler(handler)

	// 7. Execute the plan
	result, execErr := svc.ExecutePlan(execPlan)

	// 8. Handle execution errors from lower-level service
	if execErr != nil {
		if result != nil && result.ErrorCode == "CONFIG_INVALID" {
			msg := firstNonEmpty(result.ErrorMsg, execErr.Error())
			_ = sink.Emit(newEvent("error", "execute", "CONFIG_INVALID", msg))
			s.persistExecuteErrorGlobal("CONFIG_INVALID", msg)
		}
		return Result{
				PlanID:       planID,
				RootPath:     slashRootPath,
				Status:       "failed",
				ErrorCode:    firstNonEmpty(getExecResultErrorCode(result), "EXECUTION_FAILED"),
				ErrorMessage: firstNonEmpty(getExecResultErrorMsg(result), execErr.Error()),
			}, NewError(ErrKindFailedPrecondition,
				firstNonEmpty(getExecResultErrorCode(result), "EXECUTE_FAILED"),
				fmt.Sprintf("execute plan failed: %v", execErr), execErr)
	}

	// 9. Usecase-owned outcome decision: if all folders failed, treat as overall failure.
	// The lower-level service reports per-item facts; the usecase decides overall outcome.
	if handler.hasAnyFailedFolder() {
		allFolders := planFolders(execPlan)
		nonFailed := nonFailedFolders(allFolders, handler.failedFolders)
		if len(nonFailed) == 0 {
			// Every folder failed — overall failure.
			_ = sink.Emit(newEvent("error", "execute", "EXECUTION_FAILED",
				"all folders failed"))
			return Result{
					PlanID:       planID,
					RootPath:     slashRootPath,
					Status:       "failed",
					ErrorCode:    "EXECUTION_FAILED",
					ErrorMessage: "all folders failed",
				}, NewError(ErrKindFailedPrecondition, "EXECUTION_FAILED",
					"all folders failed", nil)
		}
	}

	// 10. Emit completed event
	_ = sink.Emit(Event{
		Type:            "completed",
		Message:         fmt.Sprintf("Execution complete: %s", result.Status),
		ProgressPercent: 100,
		PlanID:          planID,
		RootPath:        slashRootPath,
		EventID:         generateEventID(),
		Timestamp:       time.Now(),
	})

	return Result{
		PlanID:   planID,
		RootPath: slashRootPath,
		Status:   result.Status,
	}, nil
}

// getExecResultErrorCode extracts ErrorCode from an ExecuteResult, returning empty string if nil.
func getExecResultErrorCode(r *exesvc.ExecuteResult) string {
	if r == nil {
		return ""
	}
	return r.ErrorCode
}

// getExecResultErrorMsg extracts ErrorMsg from an ExecuteResult, returning empty string if nil.
func getExecResultErrorMsg(r *exesvc.ExecuteResult) string {
	if r == nil {
		return ""
	}
	return r.ErrorMsg
}

// hasConvertOp checks if the plan has any convert (non-delete) operations.
func hasConvertOp(plan *exesvc.Plan) bool {
	for _, item := range plan.Items {
		if item.Type == exesvc.ItemTypeConvert {
			return true
		}
	}
	return false
}

// Helper to check if a db item op_type is a convert operation.
func isConvertOpType(opType string) bool {
	return strings.EqualFold(opType, "convert_and_delete")
}

// planFolders extracts distinct folder paths from the plan items using attributeFolderPath.
func planFolders(plan *exesvc.Plan) []string {
	if plan == nil || plan.RootPath == "" {
		return nil
	}
	seen := make(map[string]bool)
	var folders []string
	for _, item := range plan.Items {
		f := attributeFolderPath(plan.RootPath, firstNonEmpty(item.SourcePath, item.PreconditionPath, item.TargetPath))
		if f != "" && !seen[f] {
			seen[f] = true
			folders = append(folders, f)
		}
	}
	return folders
}
