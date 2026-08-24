//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
)

func waitForParentExit(ctx context.Context, parentPID int) error {
	if parentPID <= 0 {
		return fmt.Errorf("invalid parent pid: %d", parentPID)
	}

	process, err := os.FindProcess(parentPID)
	if err != nil {
		return fmt.Errorf("find parent process: %w", err)
	}

	resultCh := make(chan error, 1)
	go func() {
		_, waitErr := process.Wait()
		resultCh <- waitErr
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-resultCh:
		return err
	}
}
