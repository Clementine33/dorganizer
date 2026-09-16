package workset

import "github.com/onsei/organizer/backend/internal/repo/sqlite"

type serviceImpl struct {
	repo       *sqlite.Repository
	dispatcher *dispatcher
	tasks      []Task
	// scanFolder refreshes a member folder's inventory before a session plans
	// or writes. Nil keeps the stored inventory as the only input facts.
	scanFolder FolderScan
}

// NewService creates the workset usecase service. The registered tasks are the
// wiring point for task kinds: creation materializes one operation and seed
// draft per entry, in this order, and every operation-scoped call resolves its
// task from this list. scanFolder is the disk → inventory seam every session
// uses to refresh its member folders before planning or executing.
func NewService(repo *sqlite.Repository, concurrency int, tasks []Task, scanFolder FolderScan) Service {
	s := &serviceImpl{
		repo:       repo,
		tasks:      tasks,
		scanFolder: scanFolder,
	}
	s.dispatcher = newDispatcher(s, concurrency)
	return s
}

// DispatcherHandle exposes the background dispatcher for main wiring.
func (s *serviceImpl) DispatcherHandle() Dispatcher {
	return s.dispatcher
}
