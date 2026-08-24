package execute

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

func (s *ExecuteService) processConvertJob(plan *Plan, sessionID string, item PlanItem, itemIndex int, runtime poolRuntime) error {
	src := item.SourcePath
	if src == "" {
		src = item.Src
	}
	dst := item.TargetPath
	if dst == "" {
		dst = item.Dst
	}

	tmpOut := filepath.Join(s.scratchRoot, "out", sessionID, filepath.Base(dst)+".pool."+uuid.NewString()[:8])
	if err := os.MkdirAll(filepath.Dir(tmpOut), 0755); err != nil {
		return &stageFailureError{stage: "stage3", itemIndex: itemIndex, err: fmt.Errorf("failed to create tmp directory: %w", err)}
	}
	defer func() {
		_ = os.Remove(tmpOut)
	}()

	if runtime.runEncoderToTmpFn == nil {
		runtime.runEncoderToTmpFn = func(srcPath, tmpPath string, rt poolRuntime) error {
			return s.runEncoderToTmp(srcPath, tmpPath, rt)
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

	if err := runtime.runEncoderToTmpFn(src, tmpOut, runtime); err != nil {
		var openErr *sourceOpenError
		if errors.As(err, &openErr) {
			return &stageFailureError{stage: "stage1", itemIndex: itemIndex, err: openErr}
		}
		return &stageFailureError{stage: "stage2", itemIndex: itemIndex, err: err}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return &stageFailureError{stage: "stage3", itemIndex: itemIndex, err: fmt.Errorf("failed to create dst directory: %w", err)}
	}

	runtime.ioSem.Acquire()
	commitErr := runtime.commitReplaceFn(tmpOut, dst)
	runtime.ioSem.Release()
	if commitErr != nil {
		return &stageFailureError{stage: "stage3", itemIndex: itemIndex, err: commitErr}
	}

	return nil
}

func (s *ExecuteService) runEncoderToTmp(src, tmpOut string, runtime poolRuntime) error {
	if runtime.ioSem == nil {
		runtime.ioSem = newBoundedSem(s.maxIOWorkers())
	}
	if runtime.cpuSem == nil {
		runtime.cpuSem = newBoundedSem(maxCPUWorkers())
	}

	toolPath, args, err := s.encoderCommandArgs()
	if err != nil {
		return err
	}

	runtime.ioSem.Acquire()
	defer runtime.ioSem.Release()

	srcFile, err := os.Open(src)
	if err != nil {
		return &sourceOpenError{err: fmt.Errorf("open src: %w", err)}
	}
	defer srcFile.Close()

	outFile, err := os.Create(tmpOut)
	if err != nil {
		return fmt.Errorf("create tmpOut: %w", err)
	}

	cmd := exec.Command(toolPath, args...)
	cmd.Dir = filepath.Dir(src)
	cmd.Stdin = srcFile
	cmd.Stdout = outFile
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	runtime.cpuSem.Acquire()
	runErr := cmd.Run()
	runtime.cpuSem.Release()

	if runErr != nil {
		_ = outFile.Close()
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("encoder run failed: %w: %s", runErr, msg)
		}
		return fmt.Errorf("encoder run failed: %w", runErr)
	}

	if err := outFile.Sync(); err != nil {
		_ = outFile.Close()
		return fmt.Errorf("sync tmpOut: %w", err)
	}
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("close tmpOut: %w", err)
	}

	return nil
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

func (s *ExecuteService) encoderCommandArgs() (string, []string, error) {
	encoder := strings.ToLower(strings.TrimSpace(s.toolsConfig.Encoder))
	switch encoder {
	case "qaac":
		if s.toolsConfig.QAACPath == "" {
			return "", nil, fmt.Errorf("qaac selected but qaac_path is not configured")
		}
		return s.toolsConfig.QAACPath, []string{"--ignorelength", "--no-optimize", "-s", "-v", "256", "-o", "-", "-"}, nil
	case "lame":
		if s.toolsConfig.LAMEPath == "" {
			return "", nil, fmt.Errorf("lame selected but lame_path is not configured")
		}
		return s.toolsConfig.LAMEPath, []string{"-b", "320", "-", "-"}, nil
	default:
		return "", nil, fmt.Errorf("invalid encoder: %s", s.toolsConfig.Encoder)
	}
}
