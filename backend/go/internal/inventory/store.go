package inventory

// pipelineRepo is the optional half of the staging store: a store that can
// write whole batches and clean a session up gets those paths used; one that
// cannot falls back to single-entry writes and to the session status.
type pipelineRepo interface {
	WriteStagingBatch(sessionID string, entries []StagingEntry) error
	CleanupStagingSession(sessionID string) error
}
