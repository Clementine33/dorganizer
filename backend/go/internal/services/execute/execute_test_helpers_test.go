package execute

import (
	"os"
	"sync"
)

type mockBatchBarrierRunner struct {
	mu              sync.Mutex
	convertFailures map[string]error
	deleteFailures  map[string]error
	convertCalls    []string
	deleteCalls     []string
}

func newMockBatchBarrierRunner() *mockBatchBarrierRunner {
	return &mockBatchBarrierRunner{
		convertFailures: map[string]error{},
		deleteFailures:  map[string]error{},
	}
}

func (m *mockBatchBarrierRunner) Convert(src, dst string) error {
	m.mu.Lock()
	m.convertCalls = append(m.convertCalls, src)
	err := m.convertFailures[src]
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(dst, []byte("mock converted"), 0644)
}

func (m *mockBatchBarrierRunner) Delete(path string, soft bool) error {
	m.mu.Lock()
	m.deleteCalls = append(m.deleteCalls, path)
	err := m.deleteFailures[path]
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (m *mockBatchBarrierRunner) getDeleteCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.deleteCalls))
	copy(out, m.deleteCalls)
	return out
}

func (m *mockBatchBarrierRunner) getConvertCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.convertCalls))
	copy(out, m.convertCalls)
	return out
}

// mockEventHandler tracks callback invocations for testing event routing
type mockEventHandler struct {
	mu                        sync.Mutex
	onDeleteFailedCalls       []int    // item indices where OnDeleteFailed was called
	onStage3CommitFailedCalls []int    // item indices where OnStage3CommitFailed was called
	onStage2EncodeFailedCalls []int    // item indices where OnStage2EncodeFailed was called
	onStage1CopyFailedCalls   []int    // item indices where OnStage1CopyFailed was called
	onPreconditionFailedCalls []int    // item indices where OnPreconditionFailed was called
	onFolderCompletedCalls    []string // folder paths where OnFolderCompleted was called
}

func newMockEventHandler() *mockEventHandler {
	return &mockEventHandler{}
}

func (m *mockEventHandler) OnPreconditionFailed(itemIndex int, item PlanItem, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onPreconditionFailedCalls = append(m.onPreconditionFailedCalls, itemIndex)
}

func (m *mockEventHandler) OnStage1CopyFailed(itemIndex int, item PlanItem, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStage1CopyFailedCalls = append(m.onStage1CopyFailedCalls, itemIndex)
}

func (m *mockEventHandler) OnStage2EncodeFailed(itemIndex int, item PlanItem, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStage2EncodeFailedCalls = append(m.onStage2EncodeFailedCalls, itemIndex)
}

func (m *mockEventHandler) OnStage3CommitFailed(itemIndex int, item PlanItem, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStage3CommitFailedCalls = append(m.onStage3CommitFailedCalls, itemIndex)
}

func (m *mockEventHandler) OnDeleteFailed(itemIndex int, item PlanItem, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDeleteFailedCalls = append(m.onDeleteFailedCalls, itemIndex)
}

func (m *mockEventHandler) OnFolderCompleted(folderPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFolderCompletedCalls = append(m.onFolderCompletedCalls, folderPath)
}

func (m *mockEventHandler) getDeleteFailedCalls() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, len(m.onDeleteFailedCalls))
	copy(out, m.onDeleteFailedCalls)
	return out
}

func (m *mockEventHandler) getStage3CommitFailedCalls() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, len(m.onStage3CommitFailedCalls))
	copy(out, m.onStage3CommitFailedCalls)
	return out
}

func (m *mockEventHandler) getStage2EncodeFailedCalls() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, len(m.onStage2EncodeFailedCalls))
	copy(out, m.onStage2EncodeFailedCalls)
	return out
}

func (m *mockEventHandler) getStage1CopyFailedCalls() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, len(m.onStage1CopyFailedCalls))
	copy(out, m.onStage1CopyFailedCalls)
	return out
}

func (m *mockEventHandler) getFolderCompletedCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.onFolderCompletedCalls))
	copy(out, m.onFolderCompletedCalls)
	return out
}
