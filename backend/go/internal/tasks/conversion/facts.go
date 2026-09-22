package conversion

import (
	"encoding/json"
	"sort"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
	"github.com/onsei/organizer/backend/internal/workset"
)

// rootIsStale compares one persisted root fingerprint against the current
// scanned inventory using only audio entries (same normalized collection and
// filtering used at planning time). A missing root whose inventory remains
// empty is not stale (it is represented by root_status/SOURCE_MISSING).
// A collection failure is never "valid": fail closed toward stale.
func rootIsStale(inv Inventory, r workset.PlanRootRecord) bool {
	if r.RootPath == "" {
		return false
	}
	entries, err := collectRootEntries(inv, r.RootPath)
	if err != nil {
		return true
	}
	audio := reconcile.AudioEntries(entries)
	digest, count := reconcile.InventoryFingerprint(audio)
	return digest != r.InventoryFingerprint || count != r.EntryCount
}

// collectRootEntries loads recognized audio entries under a planning root with
// the metadata needed for fingerprinting, plus the generation credential of any
// file this app wrote (same semantics as the planner's own collection).
// collectRootEntries loads the observed audio entries under a planning root
// with the generation credentials recorded for them, and maps them onto the
// planning fact set. The reads and their null handling belong to the inventory
// adapter; the mapping onto a planning fact — including which paths count once
// and the order they are planned in — belongs here.
func collectRootEntries(inv Inventory, root string) ([]reconcile.AudioEntry, error) {
	observed, err := inv.ObservedAudioEntries(normalizeScopePath(root))
	if err != nil {
		return nil, err
	}

	entries := make([]reconcile.AudioEntry, 0, len(observed))
	seen := map[string]struct{}{}
	for _, o := range observed {
		if _, ok := seen[o.Path]; ok {
			continue
		}
		seen[o.Path] = struct{}{}
		entry := reconcile.AudioEntry{
			PathPosix: o.Path,
			Size:      o.Size,
			Mtime:     o.Mtime,
			Bitrate:   o.Bitrate,
			Format:    o.Format,
		}
		if o.Generated != nil {
			entry.Generated = &reconcile.GeneratedFacts{
				Codec:       reconcile.Codec(o.Generated.Codec),
				BitrateKbps: o.Generated.BitrateKbps,
				Mode:        o.Generated.Mode,
				Size:        o.Generated.Size,
				Mtime:       o.Generated.Mtime,
			}
		}
		entries = append(entries, entry)
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
