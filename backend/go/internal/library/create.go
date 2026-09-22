package library

// Create registers a library for a root path. The root's canonical identity is
// the refusal boundary: a second entry for the same root is ErrLibraryExists.
func (s *Service) Create(name, rootPath string) (*Library, error) {
	return s.store.CreateLibrary(name, rootPath)
}
