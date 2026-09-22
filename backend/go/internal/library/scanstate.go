package library

import "time"

// RecordScanState records the outcome of a scan of one library: the fact lives
// on the library row, while the scan session that produced it belongs to the
// inventory. It is how the scanning path writes back without owning this row.
func (s *Service) RecordScanState(libraryID, status, message string, at time.Time) error {
	return s.store.UpdateLibraryScanState(libraryID, status, message, at)
}
