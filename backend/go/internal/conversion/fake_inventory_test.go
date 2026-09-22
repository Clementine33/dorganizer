package conversion //nolint:testpackage // shares the unexported analyzer tests it stands in for

import (
	"sync"

	"github.com/onsei/organizer/backend/internal/inventory"
)

// recordingInventory stands in for the inventory port in the planner's own
// tests: it remembers what it was handed and answers with whatever the test set
// up. The write shape behind the port — per row, or chunked into one
// transaction — belongs to the adapter that owns the table, and is tested there.
type recordingInventory struct {
	mu       sync.Mutex
	updates  []inventory.BitrateUpdate
	batch    []bool
	writeErr error
	observed []inventory.ObservedAudio
	readErr  error
}

func (r *recordingInventory) ObservedAudioEntries(string) ([]inventory.ObservedAudio, error) {
	return r.observed, r.readErr
}

func (r *recordingInventory) RootExistsInInventory(string) (bool, error) { return true, nil }

func (r *recordingInventory) UpdateEntryBitrates(updates []inventory.BitrateUpdate, batch bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, updates...)
	r.batch = append(r.batch, batch)
	return r.writeErr
}

// written returns the paths the writer was handed, in write order.
func (r *recordingInventory) written() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	paths := make([]string, 0, len(r.updates))
	for _, u := range r.updates {
		paths = append(paths, u.Path)
	}
	return paths
}
