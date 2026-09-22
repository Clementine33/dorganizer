package library

// Delete removes a library entry together with its processing record, plan and
// execution results — never the media, and never the recovery directory on
// disk (ADR 0001 §5).
//
// Deleting takes the direct-file-management slot, and it is taken before the
// library is looked up: a busy slot answers BUSY even for an id that does not
// exist, so the answer never depends on which check ran first (ADR 0002 §2).
func (s *Service) Delete(id string) error {
	release, err := s.beginManual()
	if err != nil {
		return err
	}
	defer release()

	return s.store.DeleteLibrary(id)
}
