package conversion

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// rootIsStale compares one persisted root fingerprint against the current
// scanned inventory using only audio entries (same normalized collection and
// filtering used at planning time). A missing root whose inventory remains
// empty is not stale (it is represented by root_status/SOURCE_MISSING).
// A collection failure is never "valid": fail closed toward stale.
func rootIsStale(repo *sqlite.Repository, r sqlite.PlanRootRecord) bool {
	if r.RootPath == "" {
		return false
	}
	entries, err := collectRootEntries(repo, r.RootPath)
	if err != nil {
		return true
	}
	audio := reconcile.AudioEntries(entries)
	digest, count := reconcile.InventoryFingerprint(audio)
	return digest != r.InventoryFingerprint || count != r.EntryCount
}

// collectRootEntries loads recognized audio entries under a planning root with
// the metadata needed for fingerprinting (same semantics as the planner's own
// collection).
func collectRootEntries(repo *sqlite.Repository, root string) ([]reconcile.AudioEntry, error) {
	rootPosix := normalizeScopePath(root)
	prefix := strings.TrimSuffix(rootPosix, "/")
	likePrefix := escapeLikePattern(prefix)
	rows, err := repo.DB().Query(`
		SELECT path, COALESCE(size, 0), COALESCE(mtime, 0), COALESCE(bitrate, 0), COALESCE(format, '')
		FROM entries WHERE is_dir = 0 AND (path = ? OR path LIKE ? ESCAPE '\')
	`, rootPosix, likePrefix+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]reconcile.AudioEntry, 0)
	seen := map[string]struct{}{}
	for rows.Next() {
		var e reconcile.AudioEntry
		if err := rows.Scan(&e.PathPosix, &e.Size, &e.Mtime, &e.Bitrate, &e.Format); err != nil {
			return nil, err
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

// componentOperationCount counts the frozen executable operations of one
// persisted outcome. An unreadable snapshot counts zero: the worker still runs
// the component and fails closed on it.
func componentOperationCount(outcomeJSON string) int {
	var outcome reconcile.ComponentOutcome
	if err := json.Unmarshal([]byte(outcomeJSON), &outcome); err != nil {
		return 0
	}
	return len(outcome.Operations)
}

// defaultProfile is the balanced seed shape: wav lossless plus mp3@320.
func defaultProfile() reconcile.DesiredProfile {
	return reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded: &reconcile.AudioOutputSpec{
			Codec:   reconcile.CodecMp3,
			Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
		},
	}
}
