package workset

import "github.com/onsei/organizer/backend/internal/repo/sqlite"

type serviceImpl struct {
	repo       *sqlite.Repository
	configDir  string
	dispatcher *dispatcher
	tasks      []Task
}

// NewService creates the workset usecase service. The registered tasks are the
// wiring point for task kinds: creation materializes one operation and seed
// draft per entry, in this order.
func NewService(repo *sqlite.Repository, configDir string, concurrency int) Service {
	s := &serviceImpl{
		repo:      repo,
		configDir: configDir,
		tasks:     []Task{newConversionTask(configDir)},
	}
	s.dispatcher = newDispatcher(s, concurrency)
	return s
}

// DispatcherHandle exposes the background dispatcher for main wiring.
func (s *serviceImpl) DispatcherHandle() Dispatcher {
	return s.dispatcher
}
