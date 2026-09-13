package plan

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

// persistSingleActionPlan persists an explicit single delete/convert plan with
// precondition snapshots for its operation sources.
func persistSingleActionPlan(
	repo *sqlite.Repository,
	planID, planType, rootPath, snapshotToken, libraryID string,
	sourceFiles []string,
	ops []analyze.Operation,
) error {
	tx, err := repo.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin single action tx: %w", err)
	}
	defer tx.Rollback()

	var libID any
	if libraryID != "" {
		libID = libraryID
	}
	if _, err := tx.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, library_id, plan_type, slim_mode, snapshot_token, status, plan_kind, workflow_schema_version, created_at)
		VALUES (?, ?, ?, ?, ?, NULL, ?, 'ready', 'single_action', 0, ?)
	`, planID, filepath.ToSlash(rootPath), filepath.ToSlash(rootPath), libID, planType, snapshotToken, time.Now().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert single action plan: %w", err)
	}

	sourcePaths := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.SourcePath != "" {
			sourcePaths = append(sourcePaths, filepath.ToSlash(op.SourcePath))
		}
	}
	preconds := map[string]sqlite.Precond{}
	if len(sourcePaths) > 0 {
		var err error
		preconds, err = sqlite.LoadEntryPreconditionsBatchTx(tx, sourcePaths)
		if err != nil {
			return fmt.Errorf("batch load preconditions: %w", err)
		}
	}

	items := make([]sqlite.PlanItem, 0, len(ops))
	for itemIndex, op := range ops {
		opType := "delete"
		if op.Type == analyze.OpTypeConvert {
			opType = "convert_and_delete"
		}
		prePath := filepath.ToSlash(op.SourcePath)
		p := preconds[prePath]
		var targetPath *string
		if op.TargetPath != "" {
			tp := filepath.ToSlash(op.TargetPath)
			targetPath = &tp
		}
		items = append(items, sqlite.PlanItem{
			PlanID:                 planID,
			ItemIndex:              itemIndex,
			OpType:                 opType,
			SourcePath:             prePath,
			TargetPath:             targetPath,
			ReasonCode:             op.Reason,
			PreconditionPath:       prePath,
			PreconditionContentRev: p.ContentRev,
			PreconditionSize:       p.Size,
			PreconditionMtime:      p.Mtime,
		})
	}
	if len(items) > 0 {
		if err := sqlite.CreatePlanItemsBatchTx(tx, planID, items); err != nil {
			return fmt.Errorf("batch insert plan items: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit single action plan tx: %w", err)
	}
	return nil
}
