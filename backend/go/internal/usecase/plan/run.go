package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// RunOptions customizes the shared workflow runner.
type RunOptions struct {
	// MarkMissingRoots marks planning roots that are absent from the scanned
	// inventory as root_status=missing with SOURCE_MISSING and counts them into
	// the step summary as blocked/error. Workset generation enables this.
	MarkMissingRoots bool
	// Progress is invoked after each root in request order (best effort; nil
	// skips it). CompletedRoots is 1-based at call time.
	Progress func(Progress)
	// EffectivePolicies maps a planning root to its member's effective
	// conversion config (batch draft override else default). Roots absent
	// from the map plan with the step's default policy. Used by the workset
	// worker.
	EffectivePolicies map[string]reconcile.Policy
}

// Progress is a root-level progress report for async generation. It carries
// root counts only — a fake percentage is never derived here.
type Progress struct {
	CompletedRoots int
	TotalRoots     int
	CurrentRoot    string
}

// serviceImpl carries the collaborators of the shared workflow runner.
type serviceImpl struct {
	repo      *sqlite.Repository
	configDir string
}

// WorkflowRunResult is the non-persisting outcome of RunWorkflow: everything a
// caller needs to persist a workflow snapshot and answer the review payload.
type WorkflowRunResult struct {
	RootPath      string // merged display scope (roots joined with " + ")
	Policy        reconcile.Policy
	PolicyHash    string
	Classifier    reconcile.Classifier
	Summary       reconcile.StepSummary // aggregated, with missing-root accounting
	Roots         []sqlite.WorkflowRootRecord
	StepRecords   []sqlite.WorkflowStepRecord
	Components    []sqlite.WorkflowComponentRecord
	AllComponents []reconcile.ComponentOutcome // request-order for the response payload
}

// RunWorkflow is the single reconciliation implementation behind the workset
// planning session worker. It validates the schema, resolves the
// policy/classifier, processes the ordered roots concurrently (results
// collected in request order), and returns persisted-snapshot records without
// touching the database. Callers persist via their own transaction boundary.
//
//nolint:gocognit,funlen // step-1 workflow has many outcome branches; split when steps multiply
func RunWorkflow(
	ctx context.Context,
	repo *sqlite.Repository,
	configDir string,
	wf *Workflow,
	roots []string,
	opts RunOptions,
) (*WorkflowRunResult, error) {
	if wf.SchemaVersion != WorkflowSchemaVersion && wf.SchemaVersion != WorkflowSchemaVersionV2 {
		return nil, NewError(
			ErrKindInvalidArgument,
			"INVALID_WORKFLOW_SCHEMA",
			fmt.Sprintf("unsupported workflow schema version %d", wf.SchemaVersion),
			nil,
		)
	}
	if len(wf.Steps) != 1 || wf.Steps[0].StepType != StepTypeReconcileAudio {
		return nil, NewError(
			ErrKindInvalidArgument,
			"UNSUPPORTED_STEP",
			"schema v1 supports only the reconcile_audio_outputs step",
			nil,
		)
	}
	if len(roots) == 0 {
		return nil, NewError(
			ErrKindInvalidArgument,
			"SCOPE_REQUIRED",
			"workflow requires at least one planning root",
			nil,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	step := wf.Steps[0]

	s := &serviceImpl{repo: repo, configDir: configDir}
	planCfg, cfgErr := getPlanConfig(configDir)
	if cfgErr != nil {
		planCfg = defaultPlanConfig()
	}

	policy, classifier, err := s.resolvePolicy(step.Policy)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Planning roots are independent works: analyze them concurrently (the
	// bitrate enrichment serializes its own DB writes internally). Results are
	// collected in request order so persistence is deterministic.
	type planOutcome struct {
		index   int
		root    string
		result  reconcile.ReconcileResult
		missing bool
		err     error
	}
	// effectivePolicy resolves one root's policy: the member's override when
	// present, else the step default.
	effectivePolicy := func(root string) (reconcile.Policy, *reconcile.Classifier, error) {
		if override, ok := opts.EffectivePolicies[root]; ok {
			classifier, err := reconcile.ResolveClassifier(override.ClassifierTags)
			if err != nil {
				return override, nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), err)
			}
			return override, &classifier, nil
		}
		return policy, classifier, nil
	}
	outcomes := make(chan planOutcome, len(roots))
	for i, root := range roots {
		go func(i int, root string) {
			if ctx.Err() != nil {
				outcomes <- planOutcome{index: i, root: root, err: ctx.Err()}
				return
			}
			rootPolicy, rootClassifier, polErr := effectivePolicy(root)
			if polErr != nil {
				outcomes <- planOutcome{index: i, root: root, err: polErr}
				return
			}
			entries, collectErr := collectWorkflowEntries(s.repo, root)
			if collectErr != nil {
				outcomes <- planOutcome{index: i, root: root, err: collectErr}
				return
			}
			enriched, enrichErr := enrichWorkflowBitrate(ctx, s.repo, entries, planCfg)
			if enrichErr != nil {
				outcomes <- planOutcome{index: i, root: root, err: enrichErr}
				return
			}
			result, recErr := reconcile.Reconcile(reconcile.ReconcileInput{
				RootPath:   root,
				Entries:    enriched,
				Policy:     rootPolicy,
				Classifier: *rootClassifier,
			})
			if recErr != nil {
				outcomes <- planOutcome{index: i, root: root, err: recErr}
				return
			}
			missing := false
			if opts.MarkMissingRoots && len(enriched) == 0 {
				exists, existsErr := rootExistsInInventory(s.repo, root)
				if existsErr != nil {
					outcomes <- planOutcome{index: i, root: root, err: existsErr}
					return
				}
				missing = !exists
			}
			outcomes <- planOutcome{index: i, root: root, result: result, missing: missing}
		}(i, root)
	}

	ordered := make([]planOutcome, len(roots))
	for range roots {
		o := <-outcomes
		ordered[o.index] = o
	}

	var allComponents []reconcile.ComponentOutcome
	rootRecords := make([]sqlite.WorkflowRootRecord, 0, len(roots))
	componentRecords := make([]sqlite.WorkflowComponentRecord, 0)
	aggregated := reconcile.StepSummary{}
	componentIndex := 0
	for i, o := range ordered {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if o.err != nil {
			return nil, NewError(
				ErrKindInternal,
				"COLLECT_FAILED",
				fmt.Sprintf("analyze planning root %s: %v", o.root, o.err),
				o.err,
			)
		}
		rootStatus := "ok"
		rootErrorCode := ""
		rootErrorMessage := ""
		if o.missing {
			rootStatus = "missing"
			rootErrorCode = reconcile.ReasonSourceMissing
			rootErrorMessage = "planning root not found in the scanned inventory"
		}
		rootRecords = append(rootRecords, sqlite.WorkflowRootRecord{
			RootIndex:            i,
			RootPath:             o.root,
			RootIdentity:         o.root,
			InventoryFingerprint: o.result.Digest,
			EntryCount:           o.result.Count,
			RootStatus:           rootStatus,
			RootErrorCode:        rootErrorCode,
			RootErrorMessage:     rootErrorMessage,
		})
		for _, comp := range o.result.Components {
			componentRecords = append(componentRecords, sqlite.WorkflowComponentRecord{
				StepIndex:      0,
				ComponentIndex: componentIndex,
				ComponentID:    comp.ComponentID,
				RootIndex:      i,
				Partition:      string(comp.Partition),
				Status:         comp.Status,
				ReasonCode:     comp.ReasonCode,
				OutcomeJSON:    mustJSON(comp),
			})
			componentIndex++
		}
		allComponents = append(allComponents, o.result.Components...)
		aggregated.ComponentCount += o.result.Summary.ComponentCount
		aggregated.BlockedCount += o.result.Summary.BlockedCount
		aggregated.OperationCount += o.result.Summary.OperationCount
		aggregated.ErrorCount += o.result.Summary.ErrorCount
		if o.missing {
			// A missing member is not a blocked Component; it is a root-level
			// failure that still forces the revision conclusion to BLOCKED/PARTIAL.
			aggregated.BlockedCount++
			aggregated.ErrorCount++
		}
		if opts.Progress != nil {
			opts.Progress(Progress{
				CompletedRoots: i + 1,
				TotalRoots:     len(roots),
				CurrentRoot:    o.root,
			})
		}
	}
	aggregated.SummaryReason = aggregateSummaryReason(aggregated)

	policyJSON, _ := json.Marshal(policy)
	sum := sha256.Sum256(policyJSON)
	policyHash := hex.EncodeToString(sum[:])

	stepRecords := []sqlite.WorkflowStepRecord{{
		StepIndex:           0,
		StepType:            StepTypeReconcileAudio,
		Status:              stepStatus(aggregated),
		PolicySchemaVersion: policy.SchemaVersion,
		PolicyJSON:          string(policyJSON),
		PolicyHash:          policyHash,
		ClassifierTags:      normalizeTagSnapshot(policy.ClassifierTags),
		ClassifierHash:      classifier.Hash,
		StepSummaryJSON:     mustJSON(aggregated),
	}}

	return &WorkflowRunResult{
		RootPath:      joinRoots(roots),
		Policy:        policy,
		PolicyHash:    policyHash,
		Classifier:    *classifier,
		Summary:       aggregated,
		Roots:         rootRecords,
		StepRecords:   stepRecords,
		Components:    componentRecords,
		AllComponents: allComponents,
	}, nil
}

// rootExistsInInventory reports whether the planning root itself is present in
// the scanned entries table (directory or file row). A folder that was never
// scanned, or whose scan removed it, is treated as absent.
func rootExistsInInventory(repo *sqlite.Repository, root string) (bool, error) {
	normalized := normalizeScopePath(root)
	var n int
	err := repo.DB().QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", normalized).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check root existence: %w", err)
	}
	return n > 0, nil
}

func joinRoots(roots []string) string {
	out := ""
	var outSb253 strings.Builder
	for i, r := range roots {
		if i > 0 {
			outSb253.WriteString(" + ")
		}
		outSb253.WriteString(r)
	}
	out += outSb253.String()
	return out
}

// stepStatus maps an aggregated step summary onto the persisted step status.
func stepStatus(summary reconcile.StepSummary) string {
	if summary.BlockedCount > 0 && summary.OperationCount > 0 {
		return "partially_blocked"
	}
	if summary.BlockedCount > 0 {
		return "blocked"
	}
	return "ok"
}

// aggregateSummaryReason derives the summary reason from the step facts.
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
	ctx context.Context,
	repo *sqlite.Repository,
	entries []reconcile.AudioEntry,
	cfg planConfig,
) ([]reconcile.AudioEntry, error) {
	analyzer := analyze.NewAnalyzer(repo, cfg.FFprobePath)
	an := make([]analyze.Entry, 0, len(entries))
	for _, e := range entries {
		an = append(an, analyze.Entry{PathPosix: e.PathPosix, FileSize: e.Size, Bitrate: e.Bitrate, Format: e.Format})
	}
	if err := analyzer.EnrichScopedEntriesBitrateWithBatchOption(ctx, an, cfg.Bitrate.BatchUpdate); err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Bitrate = an[i].Bitrate
	}
	return entries, nil
}
