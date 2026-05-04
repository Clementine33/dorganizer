package execute

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	exesvc "github.com/onsei/organizer/backend/internal/services/execute"
)

// loadPlan loads a persisted plan and its items from the repository,
// converting them into a services/execute.Plan for execution.
func loadPlan(repo *sqlite.Repository, planID string, softDelete bool) (*exesvc.Plan, string, error) {
	planRow, err := repo.GetPlan(planID)
	if err != nil {
		return nil, "", err
	}

	itemRows, err := repo.ListPlanItems(planID)
	if err != nil {
		return nil, "", fmt.Errorf("list plan items: %w", err)
	}

	rootPath := filepath.FromSlash(firstNonEmpty(planRow.ScanRootPath, planRow.RootPath))
	execPlan := &exesvc.Plan{
		PlanID:     planID,
		RootPath:   rootPath,
		Items:      make([]exesvc.PlanItem, 0, len(itemRows)),
		SoftDelete: softDelete,
	}

	for _, item := range itemRows {
		execItem := exesvc.PlanItem{
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
			execItem.Type = exesvc.ItemTypeDelete
		} else {
			execItem.Type = exesvc.ItemTypeConvert
		}
		execPlan.Items = append(execPlan.Items, execItem)
	}

	return execPlan, rootPath, nil
}

// mapLoadError maps plan-loading errors to usecase errors and emits events through the sink.
func (s *serviceImpl) mapLoadError(planID string, err error, sink EventSink) *Error {
	if err == sql.ErrNoRows {
		_ = sink.Emit(newEvent("error", "execute", "PLAN_NOT_FOUND",
			fmt.Sprintf("Plan not found: %s", planID)))
		s.persistExecuteErrorGlobal("PLAN_NOT_FOUND", fmt.Sprintf("Plan not found: %s", planID))
		return NewError(ErrKindNotFound, "PLAN_NOT_FOUND",
			fmt.Sprintf("plan not found: %s", planID), err)
	}

	_ = sink.Emit(newEvent("error", "execute", "INTERNAL_ERROR",
		fmt.Sprintf("Internal error: %v", err)))
	s.persistExecuteErrorGlobal("INTERNAL_ERROR", fmt.Sprintf("load plan: %v", err))
	return NewError(ErrKindInternal, "INTERNAL_ERROR",
		fmt.Sprintf("load plan: %v", err), err)
}
