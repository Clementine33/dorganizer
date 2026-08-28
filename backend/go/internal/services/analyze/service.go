package analyze

import (
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Analyzer provides analyze operations against the repository.
type Analyzer struct {
	repo *sqlite.Repository
}

// NewAnalyzer creates a new analyzer.
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
