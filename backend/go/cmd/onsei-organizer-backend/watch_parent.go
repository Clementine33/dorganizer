package main

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
)

func startParentDeathWatchers(
	ctx context.Context,
	cancel context.CancelFunc,
	stdin io.Reader,
	parentPID int,
	parentWatcher func(context.Context, int) error,
) {
	if stdin == nil {
		stdin = io.Reader(nil)
	}

	if parentWatcher == nil {
		parentWatcher = waitForParentExit
	}

	var once sync.Once
	requestShutdown := func() {
		once.Do(cancel)
	}

	go watchStdinEOF(ctx, stdin, requestShutdown)

	if parentPID > 0 {
		go func() {
			if err := parentWatcher(ctx, parentPID); err != nil {
				if !errors.Is(err, context.Canceled) {
					log.Printf("parent watcher error: %v", err)
				}
				return
			}
			requestShutdown()
		}()
	}
}

func watchStdinEOF(ctx context.Context, stdin io.Reader, cancel context.CancelFunc) {
	if stdin == nil {
		return
	}

	buf := make([]byte, 1)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := stdin.Read(buf)
		if errors.Is(err, io.EOF) {
			cancel()
			return
		}
		if err != nil {
			log.Printf("stdin watcher read error: %v", err)
			return
		}
		if n == 0 {
			continue
		}
	}
}
