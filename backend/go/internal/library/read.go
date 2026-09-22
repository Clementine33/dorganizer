package library

// List returns every library.
func (s *Service) List() ([]*Library, error) {
	return s.store.ListLibraries()
}

// Get loads one library by id.
func (s *Service) Get(id string) (*Library, error) {
	return s.store.GetLibrary(id)
}
