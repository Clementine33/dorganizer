// Package library owns media libraries: the entries a user creates, the rules
// a root change and a deletion must satisfy, and the derivation of directory
// identity. The observed inventory a library browses belongs to package
// inventory; this package owns the library row itself (ADR 0001 §1, §5;
// ADR 0003 §2).
//
// Storage arrives as a consumer-defined port (Store) implemented by the SQLite
// adapter; admission arrives as the process-wide gate, because a root change
// and a deletion take the same slot as direct file management (ADR 0002 §2).
package library

import (
	"time"

	"github.com/onsei/organizer/backend/internal/admission"
)

// Library represents a user-facing music library.
type Library struct {
	ID             string
	Name           string
	RootPath       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastScanAt     *time.Time
	LastScanStatus string
	LastScanError  string
}

// Service applies the library use cases.
type Service struct {
	store         Store
	gate          *admission.Gate
	inventory     InventoryReader
	resolveMember ResolveMember
}

// NewService creates the library service. gate is the process-wide admission
// control; a nil gate means this process has no direct file management wired,
// which is the state every test server starts in. inventory is the read side
// the overview and the member trees browse through, resolveMember the disk
// check the member resolution applies.
func NewService(
	store Store,
	gate *admission.Gate,
	inventory InventoryReader,
	resolveMember ResolveMember,
) *Service {
	return &Service{store: store, gate: gate, inventory: inventory, resolveMember: resolveMember}
}

// beginManual takes the direct-file-management slot for a use case that
// rewrites library paths without writing files itself. The returned release
// must be called exactly once.
func (s *Service) beginManual() (func(), error) {
	if s.gate == nil {
		return func() {}, nil
	}
	return s.gate.BeginManual()
}
