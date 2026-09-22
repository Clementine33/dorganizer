package conversion

import "github.com/onsei/organizer/backend/internal/inventory"

// Inventory is the slice of the scanned inventory a planning pass reads and
// writes: the observed audio entries beneath one root, whether a root itself
// is still in the scanned inventory, and the probed bitrates written back onto
// the entries. All three are inventory facts, so the SQL, its null handling
// and the write shape stay with the adapter; the mapping onto a planning fact
// — and what a missing root means — stay here.
type Inventory interface {
	ObservedAudioEntries(rootPath string) ([]inventory.ObservedAudio, error)
	RootExistsInInventory(rootPath string) (bool, error)
	UpdateEntryBitrates(updates []inventory.BitrateUpdate, batch bool) error
}
