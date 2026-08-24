package analyze

import (
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Analyzer provides analyze operations
type Analyzer struct {
	repo *sqlite.Repository
}

// NewAnalyzer creates a new analyzer
func NewAnalyzer(repo *sqlite.Repository) *Analyzer {
	return &Analyzer{repo: repo}
}

func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "sqlite_locked")
}

// EnrichScopedEntriesBitrate enriches missing MP3 bitrates for scoped entries and persists them.
func (a *Analyzer) EnrichScopedEntriesBitrate(entries []Entry) error {
	return a.enrichMissingMP3Bitrate(entries, true)
}

// EnrichScopedEntriesBitrateWithBatchOption enriches missing MP3 bitrates with configurable persistence mode.
func (a *Analyzer) EnrichScopedEntriesBitrateWithBatchOption(entries []Entry, batchUpdate bool) error {
	return a.enrichMissingMP3Bitrate(entries, batchUpdate)
}

// AnalyzeSlim generates a slim plan from current entries
func (a *Analyzer) AnalyzeSlim(mode int) (*Plan, error) {
	// Get all entries from repo - use canonical 'path' column (POSIX format)
	rows, err := a.repo.DB().Query("SELECT path, COALESCE(file_size, size, 0), COALESCE(bitrate, 0), COALESCE(format, '') FROM entries WHERE is_dir = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.PathPosix, &e.FileSize, &e.Bitrate, &e.Format); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if mode == 2 {
		if err := a.enrichMissingMP3Bitrate(entries, true); err != nil {
			return nil, err
		}
	}

	var plan Plan
	if mode == 2 {
		plan = AnalyzeSlimMode2(entries, nil)
	} else {
		plan = AnalyzeSlimMode1(entries, nil)
	}
	plan.SnapshotToken = generateSnapshotToken()
	plan.PlanID = generatePlanID()

	return &plan, nil
}

// AnalyzePrune generates a prune plan based on pattern with target type filtering
func (a *Analyzer) AnalyzePrune(patternStr string, target PruneTarget) (*Plan, error) {
	// Get all entries from repo - use canonical 'path' column (POSIX format)
	rows, err := a.repo.DB().Query("SELECT path, COALESCE(file_size, size, 0), COALESCE(bitrate, 0), COALESCE(format, '') FROM entries WHERE is_dir = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.PathPosix, &e.FileSize, &e.Bitrate, &e.Format); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Use AnalyzePrune which filters by regex pattern and target type
	ops, pruneErrors := AnalyzePrune(entries, patternStr, target)

	// Build plan with operations and any errors
	plan := Plan{
		PlanID:        generatePlanID(),
		SnapshotToken: generateSnapshotToken(),
		Operations:    ops,
		Errors:        pruneErrors,
	}

	return &plan, nil
}
