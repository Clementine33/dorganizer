package grpc

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/services/analyze"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PlanOperations generates a plan for the given source files and target format.
func (s *OnseiServer) PlanOperations(_ context.Context, req *pb.PlanOperationsRequest) (*pb.PlanOperationsResponse, error) {
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

	planType := req.GetPlanType()
	targetFormat := req.GetTargetFormat()
	if planType == "" {
		planType = derivePlanType(targetFormat)
	}

	planCfg, cfgErr := s.getPlanConfig()
	if cfgErr != nil {
		planCfg = defaultPlanConfig()
	}

	if planType != "prune" && planCfg.Slim.RequireScope && len(req.GetSourceFiles()) == 0 && req.GetFolderPath() == "" && len(req.GetFolderPaths()) == 0 {
		return globalNoScopePlanResponse(), nil
	}

	analyzer := analyze.NewAnalyzer(s.repo)
	var plan *analyze.Plan
	var err error
	var planErrors []*pb.FolderError
	var successfulFolders []string
	var slimPurePattern *regexp.Regexp

	if planType == "slim" && (strings.EqualFold(targetFormat, "slim:mode2") || strings.EqualFold(targetFormat, "slim:mode1")) && req.GetPruneMatchedExcluded() {
		purePattern, patternErr := s.getPruneRegexPattern()
		if patternErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "slim prune pattern unavailable: %v", patternErr)
		}

		slimPurePattern, err = regexp.Compile(purePattern)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid slim prune regex pattern: %v", err)
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

		pruneRegex, err := s.getPruneRegexPattern()
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "prune regex config unavailable: %v", err)
		}

		if len(req.GetFolderPaths()) == 0 && req.GetFolderPath() == "" && len(req.GetSourceFiles()) == 0 {
			planDBQueryCount++ // AnalyzePrune loads entries via DB when no explicit scope.
			plan, err = analyzer.AnalyzePrune(pruneRegex, pruneTarget)
			if err != nil {
				recordSQLiteBusyLocked(err)
				return nil, status.Errorf(codes.Internal, "analyze: %v", err)
			}
		} else {
			entries, err := s.collectPruneEntriesByScopes(req.GetFolderPaths(), req.GetFolderPath(), req.GetSourceFiles())
			if err != nil {
				recordSQLiteBusyLocked(err)
				return nil, status.Errorf(codes.Internal, "collect prune entries: %v", err)
			}
			scopeEntriesCount = len(entries)

			ops, pruneErrors := analyze.AnalyzePrune(entries, pruneRegex, pruneTarget)
			plan = &analyze.Plan{PlanID: generatePlanID(), SnapshotToken: generateSnapshotToken(), Operations: ops, Errors: pruneErrors}
		}

		for _, pe := range plan.Errors {
			if pe.Code == "INVALID_PATTERN" {
				return nil, status.Errorf(codes.InvalidArgument, "invalid prune regex pattern: %s", pe.Message)
			}
		}

	case "single_delete", "single_convert":
		if len(req.SourceFiles) == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "source_files required for single_delete/single_convert")
		}
		scopeEntriesCount = len(req.SourceFiles)
		ops, err := s.buildSingleFileOperations(req.SourceFiles, targetFormat, planType)
		if err != nil {
			return nil, err
		}
		plan = &analyze.Plan{PlanID: generatePlanID(), SnapshotToken: generateSnapshotToken(), Operations: ops}

	default:
		var rootPath string
		var folderOps []analyze.Operation

		if planType == "slim" {
			if len(req.FolderPaths) > 0 {
				rootPath, folderOps, planErrors, successfulFolders, err = s.analyzeFoldersWithErrors(req.FolderPaths, analyzer, targetFormat, slimPurePattern, planCfg.Bitrate.BatchUpdate)
			} else if req.FolderPath != "" {
				rootPath, folderOps, planErrors, successfulFolders, err = s.analyzeFolderWithErrors(req.FolderPath, analyzer, targetFormat, slimPurePattern, planCfg.Bitrate.BatchUpdate)
			} else if len(req.SourceFiles) > 0 {
				entries, collectErr := s.collectSlimEntriesByScopes(nil, "", req.SourceFiles)
				if collectErr != nil {
					recordSQLiteBusyLocked(collectErr)
					return nil, status.Errorf(codes.Internal, "collect slim entries: %v", collectErr)
				}
				scopeEntriesCount = len(entries)

				mode := 2
				if strings.EqualFold(targetFormat, "slim:mode1") {
					mode = 1
				}

				var scoped analyze.Plan
				if mode == 2 {
					if enrichErr := analyzer.EnrichScopedEntriesBitrateWithBatchOption(entries, planCfg.Bitrate.BatchUpdate); enrichErr != nil {
						return nil, status.Errorf(codes.Internal, "analyze: failed to enrich scoped bitrate: %v", enrichErr)
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
				rootPath, folderOps, planErrors, successfulFolders, err = s.analyzeFoldersWithErrors(req.FolderPaths, analyzer, targetFormat, nil, planCfg.Bitrate.BatchUpdate)
			} else if req.FolderPath != "" {
				rootPath, folderOps, planErrors, successfulFolders, err = s.analyzeFolderWithErrors(req.FolderPath, analyzer, targetFormat, nil, planCfg.Bitrate.BatchUpdate)
			} else {
				planDBQueryCount++ // AnalyzeSlim loads entries via DB for global scan.
				plan, err = analyzer.AnalyzeSlim(1)
			}
		}

		if err != nil {
			recordSQLiteBusyLocked(err)
			return nil, status.Errorf(codes.Internal, "analyze: %v", err)
		}

		if plan == nil {
			plan = &analyze.Plan{PlanID: generatePlanID(), SnapshotToken: generateSnapshotToken(), Operations: folderOps}
			_ = rootPath
		}
	}

	if err != nil {
		recordSQLiteBusyLocked(err)
		return nil, status.Errorf(codes.Internal, "analyze: %v", err)
	}

	if planType == "slim" {
		toolsCfg, cfgErr := s.getToolsConfig()
		if cfgErr == nil {
			rewriteConvertTargetsToExt(plan, encoderTargetExt(toolsCfg.Encoder))
		}
	}

	rootResolveStart := time.Now()
	planDBQueryCount++ // determineRootPath performs DB lookups in typical paths.
	rootPath := s.determineRootPath(req, plan, planCfg.RootResolve.Batch)
	planRootResolveMs = time.Since(rootResolveStart).Milliseconds()
	s.computeDeleteTargetPaths(plan, rootPath)

	if len(plan.Errors) > 0 {
		timestamp := time.Now().Format(time.RFC3339Nano)
		failedByFolder := make(map[string]*pb.FolderError)

		for _, pe := range plan.Errors {
			if pe.Code == "" {
				continue
			}
			folderPath := attributeFolderPath(rootPath, pe.Path)
			if _, exists := failedByFolder[folderPath]; exists {
				continue
			}
			failedByFolder[folderPath] = &pb.FolderError{Stage: "plan", Code: pe.Code, Message: pe.Message, FolderPath: folderPath, RootPath: rootPath, Timestamp: timestamp, EventId: generateEventID()}
		}

		existing := make(map[string]bool)
		for _, pe := range planErrors {
			existing[pe.GetFolderPath()+"|"+pe.GetCode()] = true
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
	for _, pe := range planErrors {
		pe.PlanId = planID
	}

	planDBQueryCount++ // persistPlan performs DB writes/reads in a transaction.
	if err := s.persistPlan(planID, req, plan, planType, planCfg.RootResolve.Batch); err != nil {
		recordSQLiteBusyLocked(err)
		if st, ok := status.FromError(err); ok {
			return nil, st.Err()
		}
		return nil, status.Errorf(codes.Internal, "persist plan: %v", err)
	}

	var ops []*pb.PlannedOperation
	totalCount := int32(len(plan.Operations))
	for _, op := range plan.Operations {
		ops = append(ops, &pb.PlannedOperation{SourcePath: op.SourcePath, TargetPath: op.TargetPath, OperationType: string(op.Type)})
	}

	actionableCount := int32(len(ops))
	summaryReason := "ACTIONABLE"
	if totalCount == 0 {
		summaryReason = "NO_MATCH"
	}

	return &pb.PlanOperationsResponse{
		PlanId:            planID,
		Operations:        ops,
		TotalCount:        totalCount,
		ActionableCount:   actionableCount,
		SummaryReason:     summaryReason,
		PlanErrors:        planErrors,
		SuccessfulFolders: successfulFolders,
	}, nil
}

func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "sqlite_locked")
}

func globalNoScopePlanResponse() *pb.PlanOperationsResponse {
	planID := generatePlanID()
	timestamp := time.Now().Format(time.RFC3339Nano)
	eventID := generateEventID()
	return &pb.PlanOperationsResponse{
		PlanId:          planID,
		TotalCount:      0,
		ActionableCount: 0,
		SummaryReason:   "GLOBAL_SHORT_CIRCUIT",
		PlanErrors: []*pb.FolderError{{
			Stage:      "plan",
			Code:       "GLOBAL_NO_SCOPE",
			Message:    "source_files, folder_path, or folder_paths is required",
			FolderPath: "",
			PlanId:     planID,
			Timestamp:  timestamp,
			EventId:    eventID,
		}},
	}
}
