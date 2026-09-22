package library

import "github.com/onsei/organizer/backend/internal/pathnorm"

// Patch applies a partial edit: only the fields the caller sent change, the
// rest keep the value the current row holds.
//
// A root change rebinds every member path of the library, so it takes the
// direct-file-management slot: it neither interleaves with a file operation
// nor with a scan (ADR 0002 §2; ADR 0001 §5). A name-only edit touches no path
// and needs no admission. The row is read before the slot is taken, and a busy
// slot refuses the request instead of queueing it.
func (s *Service) Patch(id string, name, rootPath *string) (*Library, error) {
	lib, err := s.store.GetLibrary(id)
	if err != nil {
		return nil, err
	}

	newName, newRoot := lib.Name, lib.RootPath
	if name != nil {
		newName = *name
	}
	if rootPath != nil {
		newRoot = *rootPath
	}

	if pathnorm.RootPathKey(newRoot) != pathnorm.RootPathKey(lib.RootPath) {
		release, err := s.beginManual()
		if err != nil {
			return nil, err
		}
		defer release()
	}

	return s.store.UpdateLibrary(id, newName, newRoot)
}
