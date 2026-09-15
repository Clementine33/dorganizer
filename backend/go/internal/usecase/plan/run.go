package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// Plan runs one conversion planning pass over the frozen input: every root is
// planned with its own effective policy — its scanned entries are collected,
// missing bitrates probed, and the audio reconciled — and the results are
// frozen into a Snapshot. Roots are planned concurrently and collected in
// request order so persistence is deterministic. The snapshot names no storage
// types; callers persist it through their own adapter.
//
//nolint:gocognit,funlen // per-root outcome branches; split when more steps appear
func Plan(ctx context.Context, repo *sqlite.Repository, configDir string, in Input) (*Snapshot, error) {
	if len(in.Roots) == 0 {
		return nil, NewError(
			ErrKindInvalidArgument,
			"SCOPE_REQUIRED",
			"planning requires at least one root",
			nil,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	planCfg, cfgErr := getPlanConfig(configDir)
	if cfgErr != nil {
		planCfg = defaultPlanConfig()
	}

	// The baseline policy is validated once. Each root only resolves its own
	// classifier here: per-root policies were validated in full when the
	// session input was frozen.
	if err := reconcile.ValidatePolicy(in.Policy); err != nil {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), err)
	}
	classifier, err := reconcile.ResolveClassifier(in.Policy.ClassifierTags)
	if err != nil {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), err)
	}
	rootClassifiers := make([]reconcile.Classifier, len(in.Roots))
	for i, r := range in.Roots {
		rootClassifier, resolveErr := reconcile.ResolveClassifier(r.Policy.ClassifierTags)
		if resolveErr != nil {
			return nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", resolveErr.Error(), resolveErr)
		}
		rootClassifiers[i] = rootClassifier
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
	outcomes := make(chan planOutcome, len(in.Roots))
	for i, root := range in.Roots {
		go func(i int, root RootInput, classifier reconcile.Classifier) {
			if ctx.Err() != nil {
				outcomes <- planOutcome{index: i, root: root.Path, err: ctx.Err()}
				return
			}
			entries, collectErr := collectRootEntries(repo, root.Path)
			if collectErr != nil {
				outcomes <- planOutcome{index: i, root: root.Path, err: collectErr}
				return
			}
			enriched, enrichErr := enrichBitrate(ctx, repo, entries, planCfg)
			if enrichErr != nil {
				outcomes <- planOutcome{index: i, root: root.Path, err: enrichErr}
				return
			}
			result, recErr := reconcile.Reconcile(reconcile.ReconcileInput{
				RootPath:   root.Path,
				Entries:    enriched,
				Policy:     root.Policy,
				Classifier: classifier,
			})
			if recErr != nil {
				outcomes <- planOutcome{index: i, root: root.Path, err: recErr}
				return
			}
			missing := false
			if in.MarkMissingRoots && len(enriched) == 0 {
				exists, existsErr := rootExistsInInventory(repo, root.Path)
				if existsErr != nil {
					outcomes <- planOutcome{index: i, root: root.Path, err: existsErr}
					return
				}
				missing = !exists
			}
			outcomes <- planOutcome{index: i, root: root.Path, result: result, missing: missing}
		}(i, root, rootClassifiers[i])
	}

	ordered := make([]planOutcome, len(in.Roots))
	for range in.Roots {
		o := <-outcomes
		ordered[o.index] = o
	}

	rootPaths := make([]string, len(in.Roots))
	for i, r := range in.Roots {
		rootPaths[i] = r.Path
	}
	snap := &Snapshot{RootPath: strings.Join(rootPaths, " + ")}

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
		facts := RootFacts{
			Index:                i,
			Path:                 o.root,
			Identity:             o.root,
			InventoryFingerprint: o.result.Digest,
			Count:                o.result.Count,
			Status:               "ok",
		}
		if o.missing {
			facts.Status = "missing"
			facts.ErrorCode = reconcile.ReasonSourceMissing
			facts.ErrorMessage = "planning root not found in the scanned inventory"
		}
		snap.Roots = append(snap.Roots, facts)
		for _, comp := range o.result.Components {
			snap.Components = append(snap.Components, PlannedComponent{
				Index:     componentIndex,
				RootIndex: i,
				Outcome:   comp,
			})
			componentIndex++
		}
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
		if in.Progress != nil {
			in.Progress(Progress{
				CompletedRoots: i + 1,
				TotalRoots:     len(in.Roots),
				CurrentRoot:    o.root,
			})
		}
	}
	aggregated.SummaryReason = aggregateSummaryReason(aggregated)

	policyJSON, _ := json.Marshal(in.Policy)
	sum := sha256.Sum256(policyJSON)
	snap.Policy = in.Policy
	snap.PolicyJSON = string(policyJSON)
	snap.PolicyHash = hex.EncodeToString(sum[:])
	snap.ClassifierTags = classifierTagSnapshot(in.Policy.ClassifierTags)
	snap.Classifier = classifier
	snap.Summary = aggregated
	snap.Status = planStatus(aggregated)
	return snap, nil
}

// classifierTagSnapshot emits the canonical persisted tag snapshot: the
// normalized set joined with NUL, so each revision stays self-describing.
func classifierTagSnapshot(tags []string) string {
	return strings.Join(reconcile.NormalizeTags(tags), "\x00")
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

// planStatus maps an aggregated summary onto the plan's persisted status.
func planStatus(summary reconcile.StepSummary) string {
	if summary.BlockedCount > 0 && summary.OperationCount > 0 {
		return "partially_blocked"
	}
	if summary.BlockedCount > 0 {
		return "blocked"
	}
	return "ok"
}

// aggregateSummaryReason derives the summary reason from the aggregated facts.
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

// collectRootEntries loads recognized audio entries under a planning root with
// the metadata needed for fingerprinting and bitrate enrichment.
func collectRootEntries(repo *sqlite.Repository, root string) ([]reconcile.AudioEntry, error) {
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
		return nil, fmt.Errorf("query root entries: %w", err)
	}
	defer rows.Close()

	entries := make([]reconcile.AudioEntry, 0)
	seen := map[string]struct{}{}
	for rows.Next() {
		var e reconcile.AudioEntry
		if err := rows.Scan(&e.PathPosix, &e.Size, &e.Mtime, &e.Bitrate, &e.Format); err != nil {
			return nil, fmt.Errorf("scan root entry: %w", err)
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

// enrichBitrate probes the missing MP3/AAC bitrates of the entries and
// persists them, returning the entries with the probed values applied.
func enrichBitrate(
	ctx context.Context,
	repo *sqlite.Repository,
	entries []reconcile.AudioEntry,
	cfg planConfig,
) ([]reconcile.AudioEntry, error) {
	analyzer := newBitrateAnalyzer(repo, cfg.FFprobePath)
	if err := analyzer.enrichMissing(ctx, entries, cfg.Bitrate.BatchUpdate); err != nil {
		return nil, err
	}
	return entries, nil
}
