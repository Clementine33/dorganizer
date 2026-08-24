package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartParentDeathWatchers_CancelsOnStdinEOF(t *testing.T) {
	ctx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	var cancelCalls atomic.Int32
	cancel := func() {
		cancelCalls.Add(1)
		baseCancel()
	}

	startParentDeathWatchers(ctx, cancel, strings.NewReader(""), 0, nil)

	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected context cancellation from stdin EOF")
	}
}

func TestStartParentDeathWatchers_CancelsOnParentExit(t *testing.T) {
	ctx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	r, _ := io.Pipe()
	defer r.Close()

	cancel := func() {
		baseCancel()
	}

	startParentDeathWatchers(ctx, cancel, r, 123, func(context.Context, int) error {
		return nil
	})

	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected context cancellation from parent watcher")
	}
}

func TestStartParentDeathWatchers_DoesNotCancelOnParentWatchError(t *testing.T) {
	ctx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	r, _ := io.Pipe()
	defer r.Close()

	startParentDeathWatchers(ctx, baseCancel, r, 123, func(context.Context, int) error {
		return errors.New("watch failed")
	})

	select {
	case <-ctx.Done():
		t.Fatal("did not expect cancellation on parent watcher error")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStartParentDeathWatchers_OnlyCancelsOnce(t *testing.T) {
	ctx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	var cancelCalls atomic.Int32
	cancel := func() {
		cancelCalls.Add(1)
		baseCancel()
	}

	startParentDeathWatchers(ctx, cancel, strings.NewReader(""), 123, func(context.Context, int) error {
		return nil
	})

	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected context cancellation")
	}

	time.Sleep(20 * time.Millisecond)
	if got := cancelCalls.Load(); got != 1 {
		t.Fatalf("expected cancel to be called once, got %d", got)
	}
}
