package library

import "time"

// Store is the library storage this package needs: exactly the operations the
// use cases below call, no per-table repository and no read the library does
// not make. The SQLite adapter satisfies it with its one concrete Repository.
type Store interface {
	// CreateLibrary inserts a library for a root path.
	CreateLibrary(name, rootPath string) (*Library, error)
	// ListLibraries returns every library, newest first.
	ListLibraries() ([]*Library, error)
	// GetLibrary loads one library by id.
	GetLibrary(id string) (*Library, error)
	// UpdateLibrary applies the new name and root path to one library.
	UpdateLibrary(id, name, rootPath string) (*Library, error)
	// DeleteLibrary removes the library together with its record and sessions.
	DeleteLibrary(id string) error
	// UpdateLibraryScanState records the outcome of a scan of this library.
	UpdateLibraryScanState(libraryID, status, message string, at time.Time) error
}
