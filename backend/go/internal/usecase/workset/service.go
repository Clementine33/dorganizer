package workset

import "github.com/onsei/organizer/backend/internal/repo/sqlite"

type serviceImpl struct {
	repo       *sqlite.Repository
	configDir  string
	dispatcher *dispatcher
}

// NewService creates the workset usecase service.
func NewService(repo *sqlite.Repository, configDir string, concurrency int) Service {
	s := &serviceImpl{repo: repo, configDir: configDir}
	s.dispatcher = newDispatcher(s, concurrency)
	return s
}

// DispatcherHandle exposes the background dispatcher for main wiring.
func (s *serviceImpl) DispatcherHandle() Dispatcher {
	return s.dispatcher
}
