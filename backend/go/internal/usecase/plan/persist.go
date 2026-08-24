package plan

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

func persistPlan(repo *sqlite.Repository, planID string, req Request, plan *analyze.Plan, planType string, useBatchRootResolve bool) error {
	resolveRootFromEntries := func(tx *sql.Tx, path string) (string, error) {
		pathPosix := filepath.ToSlash(path)
		if pathPosix == "" {
			return "", nil
		}
		prefix := strings.TrimSuffix(pathPosix, "/") + "/%"
		var resolved string
		err := tx.QueryRow("SELECT root_path FROM entries WHERE path = ? OR path LIKE ? LIMIT 1", pathPosix, prefix).Scan(&resolved)
		if err == sql.ErrNoRows {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return resolved, nil
	}

	tx, err := repo.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	rootPath := ""
	if useBatchRootResolve {
		candidates := collectDetermineRootCandidates(req, plan)
		resolved, err := resolveRootPathFromExactMatches(candidates, func(chunk []string) (map[string]string, error) {
			return loadRootPathsByExactMatch(func(query string, args ...any) (*sql.Rows, error) {
				return tx.Query(query, args...)
			}, chunk)
		})
		if err != nil {
			return err
		}
		rootPath = resolved
	}

	if rootPath == "" {
		for _, candidate := range collectDetermineRootOperationSourceCandidates(req, plan) {
			resolved, err := resolveRootFromEntries(tx, candidate)
			if err != nil {
				return err
			}
			if resolved != "" {
				rootPath = resolved
				break
			}
		}
	}

	if rootPath == "" && req.FolderPath != "" {
		resolved, err := resolveRootFromEntries(tx, req.FolderPath)
		if err != nil {
			return err
		}
		if resolved != "" {
			rootPath = resolved
		}
	}
	if rootPath == "" {
		for _, scope := range req.FolderPaths {
			resolved, err := resolveRootFromEntries(tx, scope)
			if err != nil {
				return err
			}
			if resolved != "" {
				rootPath = resolved
				break
			}
		}
	}

	if rootPath == "" {
		rootPath = req.FolderPath
	}
	if rootPath == "" && len(req.FolderPaths) > 0 {
		rootPath = req.FolderPaths[0]
	}
	if rootPath == "" && len(req.SourceFiles) > 0 {
		rootPath = filepath.Dir(req.SourceFiles[0])
	}

	scopeRootPath := req.FolderPath
	if scopeRootPath == "" && len(req.FolderPaths) > 0 {
		scopeRootPath = req.FolderPaths[0]
	}
	if scopeRootPath == "" && rootPath != "" {
		scopeRootPath = rootPath
	}
	if scopeRootPath == "" && len(req.SourceFiles) > 0 {
		scopeRootPath = filepath.Dir(req.SourceFiles[0])
	}
	if scopeRootPath == "" {
		scopeRootPath = rootPath
	}

	effectivePlanType := planType
	if effectivePlanType == "" {
		effectivePlanType = "slim"
	}

	planRow := &sqlite.Plan{PlanID: planID, RootPath: filepath.ToSlash(scopeRootPath), ScanRootPath: filepath.ToSlash(rootPath), PlanType: effectivePlanType, SnapshotToken: plan.SnapshotToken, Status: "ready", CreatedAt: time.Now()}
	if err := sqlite.CreatePlanTx(tx, planRow); err != nil {
		if sqlite.IsPlanIDConflictError(err) {
			return NewError(ErrKindAlreadyExists, "PLAN_ID_CONFLICT", fmt.Sprintf("PLAN_ID_CONFLICT: plan %s already exists", planID), err)
		}
		return err
	}

	sourcePaths := make([]string, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		if op.SourcePath != "" {
			sourcePaths = append(sourcePaths, filepath.ToSlash(op.SourcePath))
		}
	}

	var preconds map[string]sqlite.Precond
	if len(sourcePaths) > 0 {
		preconds, err = sqlite.LoadEntryPreconditionsBatchTx(tx, sourcePaths)
		if err != nil {
			return fmt.Errorf("batch load preconditions: %w", err)
		}
	} else {
		preconds = make(map[string]sqlite.Precond)
	}

	items := make([]sqlite.PlanItem, 0, len(plan.Operations))
	for itemIndex, op := range plan.Operations {
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
		return fmt.Errorf("commit plan persistence: %w", err)
	}

	return nil
}
