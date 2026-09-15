package workset

import "github.com/onsei/organizer/backend/internal/repo/sqlite"

type serviceImpl struct {
	repo       *sqlite.Repository
	dispatcher *dispatcher
	tasks      []Task
}

// NewService creates the workset usecase service. The registered tasks are the
// wiring point for task kinds: creation materializes one operation and seed
// draft per entry, in this order, and every operation-scoped call resolves its
// task from this list.
func NewService(repo *sqlite.Repository, concurrency int, tasks []Task) Service {
	s := &serviceImpl{
		repo:  repo,
		tasks: tasks,
	}
	s.dispatcher = newDispatcher(s, concurrency)
	return s
}

// DispatcherHandle exposes the background dispatcher for main wiring.
func (s *serviceImpl) DispatcherHandle() Dispatcher {
	return s.dispatcher
}
