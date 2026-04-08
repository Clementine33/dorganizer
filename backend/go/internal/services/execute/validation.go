package execute

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

var statPath = os.Stat

type precheckFailure struct {
	index int
	err   error
}

type precheckFolderFailure struct {
	folderPath string
	index      int
	err        error
}

// validateToolsConfig validates the tools configuration before executing any items.
// This is called only when the plan contains convert operations.
func (s *ExecuteService) validateToolsConfig() error {
	encoder := s.toolsConfig.Encoder

	// For convert plans, encoder must be specified
	if encoder == "" {
		return fmt.Errorf("encoder not configured (must be 'qaac' or 'lame')")
	}

	// Validate encoder is one of the supported types
	if encoder != "qaac" && encoder != "lame" {
		return fmt.Errorf("invalid encoder: %s (must be 'qaac' or 'lame')", encoder)
	}

	// If encoder is qaac, validate qaac_path is provided
	if encoder == "qaac" {
		if s.toolsConfig.QAACPath == "" {
			return fmt.Errorf("qaac selected but qaac_path is not configured")
		}
	}

	// If encoder is lame, validate lame_path is provided
	if encoder == "lame" {
		if s.toolsConfig.LAMEPath == "" {
			return fmt.Errorf("lame selected but lame_path is not configured")
		}
	}

	// If paths are provided, verify the tools exist
	if encoder == "qaac" && s.toolsConfig.QAACPath != "" {
		if _, err := exec.LookPath(s.toolsConfig.QAACPath); err != nil {
			return fmt.Errorf("qaac selected but qaac_path is invalid: %v", err)
		}
	}

	if encoder == "lame" && s.toolsConfig.LAMEPath != "" {
		if _, err := exec.LookPath(s.toolsConfig.LAMEPath); err != nil {
			return fmt.Errorf("lame selected but lame_path is invalid: %v", err)
		}
	}

	return nil
}

// validateConvertTargetExtensions ensures convert target suffix strictly matches configured encoder.
func (s *ExecuteService) validateConvertTargetExtensions(plan *Plan) error {
	if plan == nil {
		return nil
	}

	expectedExt := ".m4a"
	if strings.EqualFold(strings.TrimSpace(s.toolsConfig.Encoder), "lame") {
		expectedExt = ".mp3"
	}

	for i, item := range plan.Items {
		if item.Type != ItemTypeConvert {
			continue
		}
		target := item.TargetPath
		if target == "" {
			target = item.Dst
		}
		if target == "" {
			continue
		}
		if !strings.EqualFold(filepath.Ext(target), expectedExt) {
			return fmt.Errorf("convert item %d target extension mismatch: expected %s for encoder %q, got %s (%s)", i, expectedExt, s.toolsConfig.Encoder, filepath.Ext(target), target)
		}
	}

	return nil
}

// IsPlanStale checks whether any precondition no longer matches current filesystem state.
func (s *ExecuteService) IsPlanStale(plan *Plan) (bool, error) {
	failure := s.precheckPlan(plan)
	if failure != nil {
		return true, failure.err
	}
	return false, nil
}

func preconditionPathForItem(item PlanItem) string {
	path := item.PreconditionPath
	if path == "" {
		path = item.SourcePath
	}
	if path == "" {
		path = item.Src
	}
	return path
}

func (s *ExecuteService) validatePreconditionPhase1(item PlanItem) (string, error) {
	path := preconditionPathForItem(item)

	info, err := statPath(path)
	if err != nil {
		return path, fmt.Errorf("SOURCE_MISSING: %w", err)
	}
	if item.PreconditionSize > 0 && info.Size() != item.PreconditionSize {
		return path, fmt.Errorf("size mismatch: expected %d, got %d", item.PreconditionSize, info.Size())
	}
	if item.PreconditionMtime > 0 {
		expected := time.Unix(item.PreconditionMtime, 0)
		delta := info.ModTime().Sub(expected)
		if delta < 0 {
			delta = -delta
		}
		if delta > time.Second {
			return path, fmt.Errorf("mtime mismatch: expected %d, got %d", item.PreconditionMtime, info.ModTime().Unix())
		}
	}

	return path, nil
}

func (s *ExecuteService) validatePreconditionPhase2(path string, item PlanItem) error {
	if item.PreconditionContentRev <= 0 {
		return nil
	}

	provider, ok := s.repo.(contentRevProvider)
	if !ok {
		return fmt.Errorf("content_rev validation unavailable")
	}
	currentRev, err := provider.GetEntryContentRev(path)
	if err != nil {
		return fmt.Errorf("content_rev read failed: %w", err)
	}
	if currentRev != item.PreconditionContentRev {
		return fmt.Errorf("content_rev mismatch: expected %d, got %d", item.PreconditionContentRev, currentRev)
	}

	return nil
}

func (s *ExecuteService) precheckPlan(plan *Plan) *precheckFailure {
	if plan == nil || len(plan.Items) == 0 {
		return nil
	}

	if !s.config.PrecheckConcurrentStat {
		for i, item := range plan.Items {
			if err := s.validatePrecondition(item); err != nil {
				return &precheckFailure{index: i, err: err}
			}
		}
		return nil
	}

	paths := make([]string, len(plan.Items))
	maxWorkers := s.maxIOWorkers()
	if maxWorkers < 1 {
		maxWorkers = 1
	}

	g, ctx := errgroup.WithContext(context.Background())
	sem := semaphore.NewWeighted(int64(maxWorkers))

	var (
		firstMu sync.Mutex
		first   *precheckFailure
	)

	recordFirst := func(index int, err error) error {
		firstMu.Lock()
		defer firstMu.Unlock()
		if first == nil {
			first = &precheckFailure{index: index, err: err}
			return err
		}
		log.Printf("execute precheck phase1 additional error ignored: item=%d err=%v (first item=%d err=%v)", index, err, first.index, first.err)
		return nil
	}

	for i, item := range plan.Items {
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}

		i, item := i, item
		g.Go(func() error {
			defer sem.Release(1)

			path, err := s.validatePreconditionPhase1(item)
			if err != nil {
				return recordFirst(i, err)
			}

			paths[i] = path
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		firstMu.Lock()
		defer firstMu.Unlock()
		if first != nil {
			return first
		}
		return &precheckFailure{index: -1, err: err}
	}

	for i, item := range plan.Items {
		if err := s.validatePreconditionPhase2(paths[i], item); err != nil {
			return &precheckFailure{index: i, err: err}
		}
	}

	return nil
}

func (s *ExecuteService) precheckPlanByFolderConcurrent(plan *Plan) []precheckFolderFailure {
	if plan == nil || len(plan.Items) == 0 {
		return nil
	}

	byFolder := make(map[string][]int)
	folders := make([]string, 0)
	for i, item := range plan.Items {
		folder := getFolderForItem(plan.RootPath, item)
		if _, ok := byFolder[folder]; !ok {
			folders = append(folders, folder)
		}
		byFolder[folder] = append(byFolder[folder], i)
	}

	maxWorkers := s.maxIOWorkers()
	if maxWorkers < 1 {
		maxWorkers = 1
	}

	sem := semaphore.NewWeighted(int64(maxWorkers))
	ctx := context.Background()

	var (
		wg       sync.WaitGroup
		failureM sync.Mutex
		failures []precheckFolderFailure
	)

	for _, folder := range folders {
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}
		folder := folder
		indices := append([]int(nil), byFolder[folder]...)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer sem.Release(1)

			for _, idx := range indices {
				if err := s.validatePrecondition(plan.Items[idx]); err != nil {
					failureM.Lock()
					failures = append(failures, precheckFolderFailure{folderPath: folder, index: idx, err: err})
					failureM.Unlock()
					return // folder-level fail-fast
				}
			}
		}()
	}

	wg.Wait()
	sort.Slice(failures, func(i, j int) bool {
		return failures[i].index < failures[j].index
	})

	return failures
}

func (s *ExecuteService) validatePrecondition(item PlanItem) error {
	path, err := s.validatePreconditionPhase1(item)
	if err != nil {
		return err
	}
	return s.validatePreconditionPhase2(path, item)
}
