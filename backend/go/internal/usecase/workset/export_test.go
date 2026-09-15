package workset

// RegisterTasksForTest replaces the service's registered tasks so the external
// test package can exercise the creation path with more than the production
// conversion task.
func RegisterTasksForTest(svc Service, tasks []Task) {
	s, ok := svc.(*serviceImpl)
	if !ok {
		panic("workset: RegisterTasksForTest on a non-package service")
	}
	s.tasks = tasks
}
