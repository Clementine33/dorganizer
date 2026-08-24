package execute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

type itemFailure struct {
	itemIndex int
	stage     string
	err       error
}

type batchOutcome struct {
	firstFailure *itemFailure
	failures     []itemFailure
}

type semAcquireRelease interface {
	Acquire()
	Release()
}

type poolRuntime struct {
	ioSem             semAcquireRelease
	cpuSem            semAcquireRelease
	runEncoderToTmpFn func(src, tmpOut string, runtime poolRuntime) error
	commitReplaceFn   func(tmpOut, dst string) error
}

type noopSem struct{}

func (noopSem) Acquire() {}
func (noopSem) Release() {}

type boundedSem struct {
	tokens chan struct{}
}

func newBoundedSem(limit int) boundedSem {
	if limit < 1 {
		limit = 1
	}
	return boundedSem{tokens: make(chan struct{}, limit)}
}

func (s boundedSem) Acquire() {
	s.tokens <- struct{}{}
}

func (s boundedSem) Release() {
	<-s.tokens
}

func (s *ExecuteService) maxIOWorkers() int {
	if s.config.MaxIOWorkers < 1 {
		return 4
	}
	return s.config.MaxIOWorkers
}

func maxCPUWorkers() int {
	n := stage2Workers()
	if n < 1 {
		return 1
	}
	return n
}

func defaultPoolRuntime(s *ExecuteService) poolRuntime {
	return poolRuntime{
		ioSem:  newBoundedSem(s.maxIOWorkers()),
		cpuSem: newBoundedSem(maxCPUWorkers()),
		runEncoderToTmpFn: func(src, tmpOut string, runtime poolRuntime) error {
			if _, ok := s.runner.(*ToolRunner); !ok {
				return s.runner.Convert(src, tmpOut)
			}
			return s.runEncoderToTmp(src, tmpOut, runtime)
		},
		commitReplaceFn: s.commitReplace,
	}
}

func (s *ExecuteService) executeConvertPoolWithTracking(plan *Plan, sessionID string, convertItems []PlanItem, convertItemIndices []int, failedFolders, successfulFolders map[string]bool) (*ExecuteResult, error) {
	itemFolders := make([]string, len(convertItems))
	for i, item := range convertItems {
		itemFolders[i] = getFolderForItem(plan.RootPath, item)
	}

	var filteredItems []PlanItem
	var filteredIndices []int
	var filteredFolders []string
	for i, item := range convertItems {
		folder := itemFolders[i]
		if folder != "" && failedFolders[folder] {
			continue
		}
		filteredItems = append(filteredItems, item)
		filteredIndices = append(filteredIndices, convertItemIndices[i])
		filteredFolders = append(filteredFolders, folder)
	}

	if len(filteredItems) == 0 {
		return nil, nil
	}

	batchSessionID := uuid.NewString()
	batchOutDir := filepath.Join(s.scratchRoot, "out", batchSessionID)
	defer func() {
		_ = os.RemoveAll(batchOutDir)
	}()
	runtime := defaultPoolRuntime(s)
	globalFailFast := plan.RootPath == ""
	outcome := s.executeConvertBatchWithPool(plan, batchSessionID, filteredItems, filteredIndices, filteredFolders, globalFailFast, runtime)

	indexToPos := make(map[int]int, len(filteredIndices))
	for pos, idx := range filteredIndices {
		indexToPos[idx] = pos
	}

	var firstConvertErr error
	if outcome.firstFailure != nil {
		firstConvertErr = outcome.firstFailure.err
	}

	failedItems := make(map[int]bool, len(outcome.failures))
	for _, failure := range outcome.failures {
		failedItems[failure.itemIndex] = true
		pos, ok := indexToPos[failure.itemIndex]
		if !ok {
			continue
		}
		folder := filteredFolders[pos]
		if folder != "" {
			failedFolders[folder] = true
		}
		if s.eventHandler == nil {
			continue
		}
		switch failure.stage {
		case "stage1":
			s.eventHandler.OnStage1CopyFailed(failure.itemIndex, filteredItems[pos], failure.err)
		case "stage2":
			s.eventHandler.OnStage2EncodeFailed(failure.itemIndex, filteredItems[pos], failure.err)
		case "stage3":
			s.eventHandler.OnStage3CommitFailed(failure.itemIndex, filteredItems[pos], failure.err)
		}
	}

	var successItems []PlanItem
	var successIndices []int
	var successFolders []string
	for i, idx := range filteredIndices {
		if failedItems[idx] {
			continue
		}
		folder := filteredFolders[i]
		if folder != "" && failedFolders[folder] {
			continue
		}
		successItems = append(successItems, filteredItems[i])
		successIndices = append(successIndices, idx)
		successFolders = append(successFolders, folder)
	}

	var firstDeleteErr error
	if plan.RootPath == "" {
		if firstConvertErr != nil {
			return s.makeFailedResult(sessionID, plan.PlanID, "EXECUTION_FAILED", firstConvertErr.Error()), firstConvertErr
		}
		if len(successItems) > 0 {
			deleteOutcome := s.runBatchDeletes(plan, successItems, successIndices)
			if deleteOutcome.firstFailure != nil {
				firstDeleteErr = deleteOutcome.firstFailure.err
				if pos, ok := indexToPos[deleteOutcome.firstFailure.itemIndex]; ok {
					if s.eventHandler != nil {
						s.eventHandler.OnDeleteFailed(deleteOutcome.firstFailure.itemIndex, filteredItems[pos], deleteOutcome.firstFailure.err)
					}
				}
				return s.makeFailedResult(sessionID, plan.PlanID, "EXEC_DELETE_FAILED", firstDeleteErr.Error()), firstDeleteErr
			}
		}
	} else {
		type deleteBucket struct {
			items   []PlanItem
			indices []int
		}
		byFolder := make(map[string]*deleteBucket)
		for i := range successItems {
			folder := successFolders[i]
			bucket := byFolder[folder]
			if bucket == nil {
				bucket = &deleteBucket{}
				byFolder[folder] = bucket
			}
			bucket.items = append(bucket.items, successItems[i])
			bucket.indices = append(bucket.indices, successIndices[i])
		}

		for folder, bucket := range byFolder {
			deleteOutcome := s.runBatchDeletes(plan, bucket.items, bucket.indices)
			if deleteOutcome.firstFailure != nil {
				if firstDeleteErr == nil {
					firstDeleteErr = deleteOutcome.firstFailure.err
				}
				if folder != "" {
					failedFolders[folder] = true
				}
				if pos, ok := indexToPos[deleteOutcome.firstFailure.itemIndex]; ok {
					if s.eventHandler != nil {
						s.eventHandler.OnDeleteFailed(deleteOutcome.firstFailure.itemIndex, filteredItems[pos], deleteOutcome.firstFailure.err)
					}
				}
				continue
			}
			if folder != "" && !failedFolders[folder] {
				successfulFolders[folder] = true
			}
		}
	}

	if firstConvertErr != nil {
		return s.makeFailedResult(sessionID, plan.PlanID, "EXECUTION_FAILED", firstConvertErr.Error()), firstConvertErr
	}
	if firstDeleteErr != nil {
		return s.makeFailedResult(sessionID, plan.PlanID, "EXEC_DELETE_FAILED", firstDeleteErr.Error()), firstDeleteErr
	}

	for _, folder := range successFolders {
		if folder != "" && !failedFolders[folder] {
			successfulFolders[folder] = true
		}
	}

	return nil, nil
}

func (s *ExecuteService) executeConvertBatchWithPool(plan *Plan, batchSessionID string, items []PlanItem, itemIndices []int, failureDomains []string, globalFailFast bool, runtime poolRuntime) *batchOutcome {
	outcome := &batchOutcome{}
	if len(items) == 0 {
		return outcome
	}

	workerCount := maxCPUWorkers()
	if workerCount > len(items) {
		workerCount = len(items)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g, _ := errgroup.WithContext(baseCtx)

	var (
		stopped       atomic.Bool  // set atomically when admission should stop
		nextIndex     atomic.Int64 // shared atomic counter for work claiming
		failuresMu    sync.Mutex   // protects outcome.failures and outcome.firstFailure
		failedDomains sync.Map
		recordFailure func(failure itemFailure)
	)

	recordFailure = func(failure itemFailure) {
		failuresMu.Lock()
		defer failuresMu.Unlock()
		if outcome.firstFailure == nil {
			outcome.firstFailure = &failure
			if globalFailFast {
				stopped.Store(true)
				cancel()
			}
		}
		outcome.failures = append(outcome.failures, failure)
	}

	for w := 0; w < workerCount; w++ {
		g.Go(func() error {
			for {
				// Atomically claim next index; if stopped, no new claims allowed
				idx := int(nextIndex.Add(1) - 1)
				if idx >= len(items) {
					return nil
				}
				// Check stopped flag BEFORE processing this item
				if stopped.Load() {
					return nil
				}
				domain := ""
				if idx < len(failureDomains) {
					domain = failureDomains[idx]
				}
				if domain != "" {
					if _, failed := failedDomains.Load(domain); failed {
						continue
					}
				}

				item := items[idx]
				itemIndex := itemIndices[idx]

				err := s.processConvertJob(plan, batchSessionID, item, itemIndex, runtime)
				if err == nil {
					continue
				}
				if domain != "" {
					failedDomains.Store(domain, struct{}{})
				}
				var stageErr *stageFailureError
				if errors.As(err, &stageErr) {
					recordFailure(itemFailure{itemIndex: itemIndex, stage: stageErr.stage, err: stageErr})
					continue
				}
				recordFailure(itemFailure{itemIndex: itemIndex, stage: "stage3", err: err})
			}
		})
	}

	_ = g.Wait()

	return outcome
}
