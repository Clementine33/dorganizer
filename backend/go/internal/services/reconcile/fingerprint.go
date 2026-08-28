package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// InventoryFingerprint digests sorted (path, size, mtime) tuples of the
// recognized audio inventory. It detects additions, removals, moves, size
// changes, and mtime changes without reading audio bytes. Known accepted
// blind spot (matching the scanner's own Unix-second model): a rewrite that
// preserves both size and mtime (same-second write or mtime-restoring tool)
// is invisible.
func InventoryFingerprint(entries []AudioEntry) (string, int) {
	type tuple struct {
		path  string
		size  int64
		mtime int64
	}
	tuples := make([]tuple, 0, len(entries))
	for _, e := range entries {
		tuples = append(tuples, tuple{path: e.PathPosix, size: e.Size, mtime: e.Mtime})
	}
	sort.Slice(tuples, func(i, j int) bool { return tuples[i].path < tuples[j].path })

	h := sha256.New()
	for _, t := range tuples {
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", t.path, t.size, t.mtime)
	}
	return hex.EncodeToString(h.Sum(nil)), len(tuples)
}

// ComponentID derives a short-lived structural identity from the planning
// root, partition, and the sorted normalized parent-directory set. It is not
// a file-content identity: inventory changes are tracked by the inventory
// fingerprint, not by the component id.
func ComponentID(rootPath string, partition Partition, comp Component) string {
	h := sha256.New()
	h.Write([]byte(rootPath))
	h.Write([]byte{0})
	h.Write([]byte(partition))
	h.Write([]byte{0})
	h.Write([]byte(strings.Join(comp.ParentDirs(), "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}
