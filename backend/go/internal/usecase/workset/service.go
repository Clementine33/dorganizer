package workset

import "github.com/onsei/organizer/backend/internal/adapters/sqlite"

type serviceImpl struct {
	repo       *sqlite.Repository
	dispatcher *dispatcher
	tasks      []Task
	// enqueueGuard runs a session-enqueue write under the process-wide
	// file-management admission control: a direct file operation and a queued
	// planning or execution session can never be started against each other
	// (ADR 0002 §2). Nil means no direct file management is wired.
	enqueueGuard EnqueueGuard
	// scanFolder refreshes a member folder's inventory before a session plans
	// or writes. Nil keeps the stored inventory as the only input facts.
	scanFolder FolderScan
	// encodeConcurrency bounds how many encode tasks one execution session runs
	// at once, and with them how many components it keeps open. Zero means the
	// automatic value (min(4, CPU count)).
	encodeConcurrency int
}

// EnqueueGuard runs a session-enqueue write under the file-management
// admission control and refuses it while direct file management is in flight.
// A nil guard means the process has no direct file management wired.
type EnqueueGuard func(fn func() error) error

// NewService creates the workset usecase service. The registered tasks are the
// wiring point for task kinds: creating a record materializes the requested
// operation and its seed draft, and every operation-scoped call resolves its
// task from this list. scanFolder is the disk → inventory seam every session
// uses to refresh its member folders before planning or executing.
// generationConcurrency bounds the planning workers; encodeConcurrency bounds
// one execution session's shared encode pool (0 = automatic). enqueueGuard is
// the file-management admission seam for session starts.
func NewService(
	repo *sqlite.Repository,
	generationConcurrency, encodeConcurrency int,
	tasks []Task,
	scanFolder FolderScan,
	enqueueGuard EnqueueGuard,
) Service {
	s := &serviceImpl{
		repo:              repo,
		tasks:             tasks,
		scanFolder:        scanFolder,
		encodeConcurrency: encodeConcurrency,
		enqueueGuard:      enqueueGuard,
	}
	s.dispatcher = newDispatcher(s, generationConcurrency)
	return s
}

// enqueue runs one session-enqueue write under the admission guard. The write
// itself happens inside the guard's critical section, so a direct file
// operation that is already in flight refuses the start instead of racing it,
// and a start that gets through is visible to the next file operation.
func (s *serviceImpl) enqueue(fn func() error) error {
	if s.enqueueGuard == nil {
		return fn()
	}
	return s.enqueueGuard(fn)
}

// DispatcherHandle exposes the background dispatcher for main wiring.
func (s *serviceImpl) DispatcherHandle() Dispatcher {
	return s.dispatcher
}
