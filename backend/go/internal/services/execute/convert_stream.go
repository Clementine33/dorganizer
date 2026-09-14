package execute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

func (s *ExecuteService) processConvertJob(
	plan *Plan,
	sessionID string,
	item PlanItem,
	itemIndex int,
	runtime poolRuntime,
) error {
	src := item.SourcePath
	if src == "" {
		src = item.Src
	}
	dst := item.TargetPath
	if dst == "" {
		dst = item.Dst
	}
	spec, err := legacyTargetSpec(dst)
	if err != nil {
		return &stageFailureError{stage: "stage2", itemIndex: itemIndex, err: err}
	}

	tmpOut := filepath.Join(
		s.scratchRoot,
		"out",
		sessionID,
		filepath.Base(dst)+".pool."+uuid.NewString()[:8],
	)
	if err := os.MkdirAll(filepath.Dir(tmpOut), 0755); err != nil {
		return &stageFailureError{
			stage:     "stage3",
			itemIndex: itemIndex,
			err:       fmt.Errorf("failed to create tmp directory: %w", err),
		}
	}
	defer func() {
		_ = os.Remove(tmpOut)
	}()

	if runtime.runEncoderToTmpFn == nil {
		runtime.runEncoderToTmpFn = func(srcPath, tmpPath string, spec reconcile.AudioOutputSpec, rt poolRuntime) error {
			return s.runEncoderToTmp(srcPath, tmpPath, spec, rt)
		}
	}
	if runtime.commitReplaceFn == nil {
		runtime.commitReplaceFn = s.commitReplace
	}
	if runtime.ioSem == nil {
		runtime.ioSem = newBoundedSem(s.maxIOWorkers())
	}
	if runtime.cpuSem == nil {
		runtime.cpuSem = newBoundedSem(maxCPUWorkers())
	}

	if err := runtime.runEncoderToTmpFn(src, tmpOut, spec, runtime); err != nil {
		if openErr, ok := errors.AsType[*sourceOpenError](err); ok {
			return &stageFailureError{stage: "stage1", itemIndex: itemIndex, err: openErr}
		}
		return &stageFailureError{stage: "stage2", itemIndex: itemIndex, err: err}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return &stageFailureError{
			stage:     "stage3",
			itemIndex: itemIndex,
			err:       fmt.Errorf("failed to create dst directory: %w", err),
		}
	}

	runtime.ioSem.Acquire()
	commitErr := runtime.commitReplaceFn(tmpOut, dst)
	runtime.ioSem.Release()
	if commitErr != nil {
		return &stageFailureError{stage: "stage3", itemIndex: itemIndex, err: commitErr}
	}

	return nil
}

func (s *ExecuteService) runEncoderToTmp(
	src, tmpOut string,
	spec reconcile.AudioOutputSpec,
	runtime poolRuntime,
) error {
	if runtime.ioSem == nil {
		runtime.ioSem = newBoundedSem(s.maxIOWorkers())
	}
	if runtime.cpuSem == nil {
		runtime.cpuSem = newBoundedSem(maxCPUWorkers())
	}

	runtime.ioSem.Acquire()
	defer runtime.ioSem.Release()

	if _, err := os.Stat(src); err != nil {
		return &sourceOpenError{err: fmt.Errorf("open src: %w", err)}
	}

	runtime.cpuSem.Acquire()
	defer runtime.cpuSem.Release()
	return newFFmpeg(s.toolsConfig).Encode(context.Background(), src, tmpOut, spec)
}

type sourceOpenError struct {
	err error
}

func (e *sourceOpenError) Error() string {
	if e == nil || e.err == nil {
		return "open src"
	}
	return e.err.Error()
}

func (e *sourceOpenError) Unwrap() error { return e.err }
