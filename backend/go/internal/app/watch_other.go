//go:build !windows

package app

import "context"

func waitForParentExit(ctx context.Context, parentPID int) error {
	<-ctx.Done()
	return ctx.Err()
}
