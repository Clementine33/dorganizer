package analyze

import (
	"context"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Analyzer provides analyze operations against the repository.
type Analyzer struct {
	repo        *sqlite.Repository
	ffprobePath string
}

// NewAnalyzer creates a new analyzer.
func NewAnalyzer(repo *sqlite.Repository, ffprobePath string) *Analyzer {
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	return &Analyzer{repo: repo, ffprobePath: ffprobePath}
}

func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked")
}

// EnrichScopedEntriesBitrate enriches missing MP3/AAC bitrates for scoped entries and persists them.
func (a *Analyzer) EnrichScopedEntriesBitrate(ctx context.Context, entries []Entry) error {
	return a.enrichMissingBitrate(ctx, entries, true)
}

// EnrichScopedEntriesBitrateWithBatchOption enriches missing MP3/AAC bitrates with configurable persistence mode.
func (a *Analyzer) EnrichScopedEntriesBitrateWithBatchOption(
	ctx context.Context,
	entries []Entry,
	batchUpdate bool,
) error {
	return a.enrichMissingBitrate(ctx, entries, batchUpdate)
}
