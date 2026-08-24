package plan

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

type serviceImpl struct {
	repo      *sqlite.Repository
	configDir string
}

// NewService creates a new plan service with real orchestration logic.
func NewService(repo *sqlite.Repository, configDir string) Service {
	return &serviceImpl{repo: repo, configDir: configDir}
}

func (s *serviceImpl) Plan(_ context.Context, req Request) (Response, error) {
	var (
		scopeEntriesCount     int
		planDBQueryCount      int
		planRootResolveMs     int64
		sqliteBusyLockedCount int
	)
	recordSQLiteBusyLocked := func(err error) {
		if isSQLiteBusyLockedError(err) {
			sqliteBusyLockedCount++
		}
	}
	defer func() {
		log.Printf("plan.scope_entries_count=%d plan.db_query_count=%d plan.root_resolve_ms=%d sqlite.busy_locked_count=%d", scopeEntriesCount, planDBQueryCount, planRootResolveMs, sqliteBusyLockedCount)
	}()

	planType := req.PlanType
	targetFormat := req.TargetFormat
	if planType == "" {
		planType = derivePlanType(targetFormat)
	}

	planCfg, cfgErr := getPlanConfig(s.configDir)
	if cfgErr != nil {
		planCfg = defaultPlanConfig()
	}

	if planType != "prune" && planCfg.Slim.RequireScope && len(req.SourceFiles) == 0 && req.FolderPath == "" && len(req.FolderPaths) == 0 {
		resp := globalNoScopeResponse()
		s.persistGlobalNoScope(resp, planType)
		return resp, nil
	}

	analyzer := analyze.NewAnalyzer(s.repo)
	var plan *analyze.Plan
	var err error
	var planErrors []*FolderError
	var successfulFolders []string
	var slimPurePattern *regexp.Regexp

	if planType == "slim" && (strings.EqualFold(targetFormat, "slim:mode2") || strings.EqualFold(targetFormat, "slim:mode1")) && req.PruneMatchedExcluded {
		purePattern, patternErr := getPruneRegexPattern(s.configDir)
		if patternErr != nil {
			return Response{}, NewError(ErrKindInvalidArgument, "CONFIG_UNAVAILABLE", fmt.Sprintf("slim prune pattern unavailable: %v", patternErr), patternErr)
		}

		slimPurePattern, err = regexp.Compile(purePattern)
		if err != nil {
			return Response{}, NewError(ErrKindInvalidArgument, "INVALID_PATTERN", fmt.Sprintf("invalid slim prune regex pattern: %v", err), err)
		}
	}

	switch planType {
	case "prune":
		pruneTarget := analyze.PruneTargetBoth
		if strings.HasPrefix(targetFormat, "prune:") {
			parts := strings.SplitN(targetFormat, ":", 2)
			if len(parts) > 1 {
				switch parts[1] {
				case "wavflac":
					pruneTarget = analyze.PruneTargetLossless
				case "mp3aac":
					pruneTarget = analyze.PruneTargetLossy
				case "both":
					pruneTarget = analyze.PruneTargetBoth
				}
			}
		}

		if len(req.FolderPaths) == 0 && req.FolderPath == "" && len(req.SourceFiles) == 0 {
			if planCfg.Slim.RequireScope {
				resp := globalNoScopeResponse()
				s.persistGlobalNoScope(resp, planType)
				return resp, nil
			}
			// Legacy: require_scope=false allows global prune scan.
			pruneRegex, err := getPruneRegexPattern(s.configDir)
			if err != nil {
				return Response{}, NewError(ErrKindInvalidArgument, "CONFIG_UNAVAILABLE", fmt.Sprintf("prune regex config unavailable: %v", err), err)
			}
			planDBQueryCount++ // AnalyzePrune loads entries via DB when no explicit scope.
			plan, err = analyzer.AnalyzePrune(pruneRegex, pruneTarget)
			if err != nil {
				recordSQLiteBusyLocked(err)
				return Response{}, NewError(ErrKindInternal, "ANALYZE_FAILED", fmt.Sprintf("analyze: %v", err), err)
			}
		} else {
			pruneRegex, err := getPruneRegexPattern(s.configDir)
			if err != nil {
				return Response{}, NewError(ErrKindInvalidArgument, "CONFIG_UNAVAILABLE", fmt.Sprintf("prune regex config unavailable: %v", err), err)
			}
			entries, err := collectEntriesByScopes(s.repo, req.FolderPaths, req.FolderPath, req.SourceFiles)
			if err != nil {
				recordSQLiteBusyLocked(err)
				return Response{}, NewError(ErrKindInternal, "COLLECT_FAILED", fmt.Sprintf("collect prune entries: %v", err), err)
			}
			scopeEntriesCount = len(entries)

			ops, pruneErrors := analyze.AnalyzePrune(entries, pruneRegex, pruneTarget)
			plan = &analyze.Plan{PlanID: generatePlanID(), SnapshotToken: generateSnapshotToken(), Operations: ops, Errors: pruneErrors}
		}

		for _, pe := range plan.Errors {
			if pe.Code == "INVALID_PATTERN" {
				return Response{}, NewError(ErrKindInvalidArgument, "INVALID_PATTERN", fmt.Sprintf("invalid prune regex pattern: %s", pe.Message), nil)
			}
		}

	case "single_delete", "single_convert":
		if len(req.SourceFiles) == 0 {
			return Response{}, NewError(ErrKindInvalidArgument, "MISSING_SOURCE_FILES", "source_files required for single_delete/single_convert", nil)
		}
		scopeEntriesCount = len(req.SourceFiles)
		ops, err := buildSingleFileOperations(req.SourceFiles, targetFormat, planType)
		if err != nil {
			return Response{}, err
		}
		plan = &analyze.Plan{PlanID: generatePlanID(), SnapshotToken: generateSnapshotToken(), Operations: ops}

	default:
		var rootPath string
		var folderOps []analyze.Operation

		if planType == "slim" {
			if len(req.FolderPaths) > 0 {
				rootPath, folderOps, planErrors, successfulFolders, err = analyzeFoldersWithErrors(s.repo, req.FolderPaths, analyzer, targetFormat, slimPurePattern, planCfg.Bitrate.BatchUpdate)
			} else if req.FolderPath != "" {
				rootPath, folderOps, planErrors, successfulFolders, err = analyzeFolderWithErrors(s.repo, req.FolderPath, analyzer, targetFormat, slimPurePattern, planCfg.Bitrate.BatchUpdate)
			} else if len(req.SourceFiles) > 0 {
				entries, collectErr := collectEntriesByScopes(s.repo, nil, "", req.SourceFiles)
				if collectErr != nil {
					recordSQLiteBusyLocked(collectErr)
					return Response{}, NewError(ErrKindInternal, "COLLECT_FAILED", fmt.Sprintf("collect slim entries: %v", collectErr), collectErr)
				}
				scopeEntriesCount = len(entries)

				mode := 2
				if strings.EqualFold(targetFormat, "slim:mode1") {
					mode = 1
				}

				var scoped analyze.Plan
				if mode == 2 {
					if enrichErr := analyzer.EnrichScopedEntriesBitrateWithBatchOption(entries, planCfg.Bitrate.BatchUpdate); enrichErr != nil {
						return Response{}, NewError(ErrKindInternal, "ENRICH_FAILED", fmt.Sprintf("analyze: failed to enrich scoped bitrate: %v", enrichErr), enrichErr)
					}
					scoped = analyze.AnalyzeSlimMode2(entries, slimPurePattern)
				} else {
					scoped = analyze.AnalyzeSlimMode1(entries, slimPurePattern)
				}

				plan = &analyze.Plan{PlanID: generatePlanID(), SnapshotToken: generateSnapshotToken(), Operations: scoped.Operations, Errors: scoped.Errors}
			} else {
				planDBQueryCount++ // AnalyzeSlim loads entries via DB for global scan.
				plan, err = analyzer.AnalyzeSlim(1)
			}
		} else {
			if len(req.FolderPaths) > 0 {
				rootPath, folderOps, planErrors, successfulFolders, err = analyzeFoldersWithErrors(s.repo, req.FolderPaths, analyzer, targetFormat, nil, planCfg.Bitrate.BatchUpdate)
			} else if req.FolderPath != "" {
				rootPath, folderOps, planErrors, successfulFolders, err = analyzeFolderWithErrors(s.repo, req.FolderPath, analyzer, targetFormat, nil, planCfg.Bitrate.BatchUpdate)
			} else {
				planDBQueryCount++ // AnalyzeSlim loads entries via DB for global scan.
				plan, err = analyzer.AnalyzeSlim(1)
			}
		}

		if err != nil {
			recordSQLiteBusyLocked(err)
			return Response{}, NewError(ErrKindInternal, "ANALYZE_FAILED", fmt.Sprintf("analyze: %v", err), err)
		}

		if plan == nil {
			plan = &analyze.Plan{PlanID: generatePlanID(), SnapshotToken: generateSnapshotToken(), Operations: folderOps}
			_ = rootPath
		}
	}

	if err != nil {
		recordSQLiteBusyLocked(err)
		return Response{}, NewError(ErrKindInternal, "ANALYZE_FAILED", fmt.Sprintf("analyze: %v", err), err)
	}

	if planType == "slim" {
		toolsCfg, cfgErr := getToolsConfig(s.configDir)
		if cfgErr == nil {
			rewriteConvertTargetsToExt(plan, encoderTargetExt(toolsCfg.Encoder))
		}
	}

	rootResolveStart := time.Now()
	planDBQueryCount++ // determineRootPath performs DB lookups in typical paths.
	rootPath := determineRootPath(s.repo, req, plan, planCfg.RootResolve.Batch)
	planRootResolveMs = time.Since(rootResolveStart).Milliseconds()
	computeDeleteTargetPaths(plan, rootPath)

	if len(plan.Errors) > 0 {
		failedByFolder := make(map[string]*FolderError)

		for _, pe := range plan.Errors {
			if pe.Code == "" {
				continue
			}
			folderPath := attributeFolderPath(rootPath, pe.Path)
			if _, exists := failedByFolder[folderPath]; exists {
				continue
			}
			failedByFolder[folderPath] = &FolderError{Code: pe.Code, Message: pe.Message, FolderPath: folderPath}
		}

		existing := make(map[string]bool)
		for _, pe := range planErrors {
			existing[pe.FolderPath+"|"+pe.Code] = true
		}
		for _, fe := range failedByFolder {
			key := fe.FolderPath + "|" + fe.Code
			if !existing[key] {
				planErrors = append(planErrors, fe)
				existing[key] = true
			}
		}

		if len(successfulFolders) > 0 {
			filtered := make([]string, 0, len(successfulFolders))
			for _, sf := range successfulFolders {
				if _, failed := failedByFolder[sf]; failed {
					continue
				}
				filtered = append(filtered, sf)
			}
			successfulFolders = filtered
		}

		if len(plan.Operations) > 0 {
			filteredOps := make([]analyze.Operation, 0, len(plan.Operations))
			for _, op := range plan.Operations {
				if op.Type == analyze.OpTypeDelete && op.TargetPath == "" {
					continue
				}
				candidate := firstNonEmpty(op.SourcePath, op.TargetPath)
				folder := attributeFolderPath(rootPath, candidate)
				if folder != "" {
					if _, failed := failedByFolder[folder]; failed {
						continue
					}
				}
				filteredOps = append(filteredOps, op)
			}
			plan.Operations = filteredOps
		}
	}

	planID := generatePlanID()
	plan.PlanID = planID

	planDBQueryCount++ // persistPlan performs DB writes/reads in a transaction.
	if err := persistPlan(s.repo, planID, req, plan, planType, planCfg.RootResolve.Batch); err != nil {
		recordSQLiteBusyLocked(err)
		if planErr, ok := err.(*Error); ok {
			return Response{}, planErr
		}
		return Response{}, NewError(ErrKindInternal, "PERSIST_FAILED", fmt.Sprintf("persist plan: %v", err), err)
	}

	// Persist plan errors into error_events (best-effort)
	for _, pe := range planErrors {
		if pe.Code == "" {
			continue
		}
		var pathPtr *string
		if fp := pe.FolderPath; fp != "" {
			pathPtr = &fp
		}
		if insertErr := s.repo.CreateErrorEvent(&sqlite.ErrorEvent{
			Scope:     planType,
			RootPath:  rootPath,
			Path:      pathPtr,
			Code:      pe.Code,
			Message:   pe.Message,
			Retryable: pe.Retryable,
		}); insertErr != nil {
			log.Printf("warning: failed to persist plan error event: %v", insertErr)
		}
	}

	// Build usecase response
	ops := make([]Operation, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		ops = append(ops, Operation{
			Type:       string(op.Type),
			SourcePath: op.SourcePath,
			TargetPath: op.TargetPath,
		})
	}

	errors := make([]FolderError, 0, len(planErrors))
	for _, pe := range planErrors {
		errors = append(errors, FolderError{
			FolderPath: pe.FolderPath,
			Code:       pe.Code,
			Message:    pe.Message,
			Retryable:  pe.Retryable,
		})
	}

	operationCount := len(ops)
	errorCount := len(errors)

	// Usecase-owned summary outcome: determine total_count, actionable_count, and summary_reason.
	totalCount := operationCount
	actionableCount := operationCount
	summaryReason := "ACTIONABLE"
	if operationCount == 0 {
		summaryReason = "NO_MATCH"
	}
	for _, pe := range errors {
		if pe.Code == "GLOBAL_NO_SCOPE" {
			summaryReason = "GLOBAL_SHORT_CIRCUIT"
			break
		}
	}

	return Response{
		PlanID:            planID,
		SnapshotToken:     plan.SnapshotToken,
		Operations:        ops,
		Errors:            errors,
		SuccessfulFolders: successfulFolders,
		RootPath:          rootPath,
		Summary: Summary{
			OperationCount:  operationCount,
			ErrorCount:      errorCount,
			TotalCount:      totalCount,
			ActionableCount: actionableCount,
			SummaryReason:   summaryReason,
		},
	}, nil
}

// globalNoScopeResponse creates a response for when no scope is provided.
func globalNoScopeResponse() Response {
	return Response{
		PlanID: generatePlanID(),
		Summary: Summary{
			OperationCount:  0,
			ErrorCount:      1,
			TotalCount:      0,
			ActionableCount: 0,
			SummaryReason:   "GLOBAL_SHORT_CIRCUIT",
		},
		Errors: []FolderError{{
			Code:    "GLOBAL_NO_SCOPE",
			Message: "source_files, folder_path, or folder_paths is required",
		}},
	}
}

// persistGlobalNoScope best-effort persists a GLOBAL_NO_SCOPE error into error_events.
func (s *serviceImpl) persistGlobalNoScope(resp Response, planType string) {
	if s.repo == nil {
		return
	}
	for _, pe := range resp.Errors {
		if pe.Code == "" {
			continue
		}
		var pathPtr *string
		if fp := pe.FolderPath; fp != "" {
			pathPtr = &fp
		}
		if err := s.repo.CreateErrorEvent(&sqlite.ErrorEvent{
			Scope:     planType,
			RootPath:  resp.RootPath,
			Path:      pathPtr,
			Code:      pe.Code,
			Message:   pe.Message,
			Retryable: false,
		}); err != nil {
			log.Printf("warning: failed to persist global no-scope error event: %v", err)
		}
	}
}
