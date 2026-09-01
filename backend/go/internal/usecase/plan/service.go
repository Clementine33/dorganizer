package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// Workflow/step constants for schema v1.
const (
	WorkflowSchemaVersion  = 1
	StepTypeReconcileAudio = "reconcile_audio_outputs"
	PlanKindWorkflow       = "workflow"
	PlanKindSingleAction   = "single_action"
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
	switch {
	case req.Workflow != nil:
		return s.planWorkflow(req)
	case req.SingleAction != nil:
		return s.planSingleAction(req)
	}
	return Response{}, NewError(
		ErrKindInvalidArgument,
		"INVALID_PLAN_REQUEST",
		"exactly one of workflow or single_action is required",
		nil,
	)
}

// planWorkflow runs the declarative reconcile_audio_outputs step over each
// planning root and persists snapshots. The reconciliation itself delegates to
// RunWorkflow so the workset planning session reuses one implementation.
func (s *serviceImpl) planWorkflow(req Request) (Response, error) {
	result, err := RunWorkflow(context.Background(), s.repo, s.configDir, req.Workflow, req.PlanningRoots, RunOptions{})
	if err != nil {
		return Response{}, err
	}

	planID := generatePlanID()
	snapshotToken := generateSnapshotToken()
	// plans.root_path carries the merged planning-root scope so a multi-root
	// plan is addressable under every root it covers (the per-root rows in
	// plan_roots hold the authoritative inventory fingerprints). The display
	// root is the first planning root.
	if err := sqlite.CreateWorkflowPlanTx(
		s.repo.DB(),
		planID,
		"workflow",
		result.RootPath,
		snapshotToken,
		req.LibraryID,
		result.StepRecords,
		result.Roots,
		result.Components,
	); err != nil {
		if sqlite.IsPlanIDConflictError(err) {
			return Response{}, NewError(
				ErrKindAlreadyExists,
				"PLAN_ID_CONFLICT",
				fmt.Sprintf("PLAN_ID_CONFLICT: plan %s already exists", planID),
				err,
			)
		}
		return Response{}, NewError(
			ErrKindInternal,
			"PERSIST_FAILED",
			fmt.Sprintf("persist workflow plan: %v", err),
			err,
		)
	}

	return Response{
		PlanID:        planID,
		SnapshotToken: snapshotToken,
		RootPath:      result.RootPath,
		PlanKind:      PlanKindWorkflow,
		Summary: Summary{
			OperationCount:  result.Summary.OperationCount,
			ErrorCount:      result.Summary.ErrorCount,
			TotalCount:      result.Summary.OperationCount,
			ActionableCount: result.Summary.OperationCount,
			SummaryReason:   result.Summary.SummaryReason,
		},
		Steps: []StepResponse{{
			StepType:   StepTypeReconcileAudio,
			StepIndex:  0,
			Status:     stepStatus(result.Summary),
			Policy:     result.Policy,
			PolicyHash: result.PolicyHash,
			Classifier: result.Classifier,
			Components: result.AllComponents,
			Summary:    result.Summary,
		}},
	}, nil
}

// planSingleAction persists an explicit delete/convert of selected source
// files. It is an independent path, never reconciled into components.
func (s *serviceImpl) planSingleAction(req Request) (Response, error) {
	action := req.SingleAction
	if action.Action != "delete" && action.Action != "convert" {
		return Response{}, NewError(
			ErrKindInvalidArgument,
			"INVALID_ACTION",
			fmt.Sprintf("unsupported single action %q", action.Action),
			nil,
		)
	}
	if len(action.SourceFiles) == 0 {
		return Response{}, NewError(
			ErrKindInvalidArgument,
			"MISSING_SOURCE_FILES",
			"source_files required for single action",
			nil,
		)
	}
	if action.Action == "convert" && action.TargetFormat == "" {
		return Response{}, NewError(
			ErrKindInvalidArgument,
			"MISSING_TARGET_FORMAT",
			"target_format required for single convert",
			nil,
		)
	}

	ops, err := buildSingleFileOperations(action.SourceFiles, action.TargetFormat, "single_"+action.Action)
	if err != nil {
		return Response{}, err
	}

	rootPath, err := resolveRootPathForSources(s.repo, action.SourceFiles)
	if err != nil {
		return Response{}, NewError(ErrKindInternal, "ROOT_RESOLVE_FAILED", fmt.Sprintf("resolve root: %v", err), err)
	}

	planID := generatePlanID()
	snapshotToken := generateSnapshotToken()
	planType := "single_" + action.Action
	if err := persistSingleActionPlan(
		s.repo,
		planID,
		planType,
		rootPath,
		snapshotToken,
		req.LibraryID,
		action.SourceFiles,
		ops,
	); err != nil {
		return Response{}, NewError(
			ErrKindInternal,
			"PERSIST_FAILED",
			fmt.Sprintf("persist single action plan: %v", err),
			err,
		)
	}

	usecaseOps := make([]Operation, 0, len(ops))
	for _, op := range ops {
		usecaseOps = append(usecaseOps, Operation{
			Type:       string(op.Type),
			SourcePath: op.SourcePath,
			TargetPath: op.TargetPath,
		})
	}
	summaryReason := reconcile.ReasonActionable
	if len(usecaseOps) == 0 {
		summaryReason = reconcile.ReasonNoMatch
	}
	return Response{
		PlanID:        planID,
		SnapshotToken: snapshotToken,
		RootPath:      rootPath,
		PlanKind:      PlanKindSingleAction,
		Operations:    usecaseOps,
		Summary: Summary{
			OperationCount:  len(usecaseOps),
			ActionableCount: len(usecaseOps),
			TotalCount:      len(usecaseOps),
			SummaryReason:   summaryReason,
		},
	}, nil
}

func stepStatus(summary reconcile.StepSummary) string {
	if summary.BlockedCount > 0 && summary.OperationCount > 0 {
		return "partially_blocked"
	}
	if summary.BlockedCount > 0 {
		return "blocked"
	}
	return "ok"
}

func aggregateSummaryReason(s reconcile.StepSummary) string {
	switch {
	case s.BlockedCount > 0 && s.OperationCount > 0:
		return reconcile.ReasonPartial
	case s.BlockedCount > 0:
		return reconcile.ReasonBlocked
	case s.OperationCount > 0:
		return reconcile.ReasonActionable
	default:
		return reconcile.ReasonNoMatch
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("plan: failed to marshal snapshot: %v", err)
		return "{}"
	}
	return string(b)
}

// collectWorkflowEntries loads recognized audio entries under a planning root
// with the metadata needed for fingerprinting and bitrate enrichment.
func collectWorkflowEntries(repo *sqlite.Repository, root string) ([]reconcile.AudioEntry, error) {
	rootPosix := normalizeScopePath(root)
	prefix := strings.TrimSuffix(rootPosix, "/")
	// LIKE patterns containing user-supplied % or _ would widen the scope;
	// escape them (same convention as collectEntriesByScopes) so a planning
	// root with such characters cannot leak sibling paths into the plan.
	likePrefix := escapeLikePattern(prefix)
	rows, err := repo.DB().Query(`
		SELECT path, COALESCE(size, 0), COALESCE(mtime, 0), COALESCE(bitrate, 0), COALESCE(format, '')
		FROM entries WHERE is_dir = 0 AND (path = ? OR path LIKE ? ESCAPE '\')
	`, rootPosix, likePrefix+"/%")
	if err != nil {
		return nil, fmt.Errorf("query workflow entries: %w", err)
	}
	defer rows.Close()

	entries := make([]reconcile.AudioEntry, 0)
	seen := map[string]struct{}{}
	for rows.Next() {
		var e reconcile.AudioEntry
		if err := rows.Scan(&e.PathPosix, &e.Size, &e.Mtime, &e.Bitrate, &e.Format); err != nil {
			return nil, fmt.Errorf("scan workflow entry: %w", err)
		}
		if _, ok := seen[e.PathPosix]; ok {
			continue
		}
		seen[e.PathPosix] = struct{}{}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].PathPosix < entries[j].PathPosix })
	return entries, nil
}

// enrichWorkflowBitrate bridges reconcile entries into the existing analyzer
// enrichment path and copies probed bitrate facts back.
func enrichWorkflowBitrate(
	repo *sqlite.Repository,
	entries []reconcile.AudioEntry,
	batch bool,
) ([]reconcile.AudioEntry, error) {
	analyzer := analyze.NewAnalyzer(repo)
	an := make([]analyze.Entry, 0, len(entries))
	for _, e := range entries {
		an = append(an, analyze.Entry{PathPosix: e.PathPosix, FileSize: e.Size, Bitrate: e.Bitrate, Format: e.Format})
	}
	if err := analyzer.EnrichScopedEntriesBitrateWithBatchOption(an, batch); err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Bitrate = an[i].Bitrate
	}
	return entries, nil
}

// resolveRootPathForSources resolves the library root for single-action
// sources via one batched query (no per-source SELECT).
func resolveRootPathForSources(repo *sqlite.Repository, sources []string) (string, error) {
	if len(sources) == 0 {
		return "", nil
	}
	normalized := make([]string, 0, len(sources))
	seen := map[string]struct{}{}
	for _, src := range sources {
		p := normalizeScopePath(src)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		normalized = append(normalized, p)
	}

	const chunkSize = 500
	for start := 0; start < len(normalized); start += chunkSize {
		end := min(start+chunkSize, len(normalized))
		chunk := normalized[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, p := range chunk {
			placeholders[i] = "?"
			args[i] = p
		}
		rows, err := repo.DB().Query(
			`SELECT root_path FROM entries WHERE path IN (`+strings.Join(placeholders, ",")+`) LIMIT 1`,
			args...)
		if err != nil {
			return "", err
		}
		var root string
		if rows.Next() {
			_ = rows.Scan(&root)
		}
		rows.Close()
		if root != "" {
			return root, nil
		}
	}
	return path.Dir(normalizeScopePath(sources[0])), nil
}
