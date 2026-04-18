package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/bootstrap"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func TestVersion_DefaultNonEmptyAndEmittedInHandshake(t *testing.T) {
	if strings.TrimSpace(version) == "" {
		t.Fatal("expected default version to be non-empty")
	}

	handshake := bootstrap.BuildHandshakeLine(43123, "token-1", version)
	if !strings.Contains(handshake, "version="+version) {
		t.Fatalf("expected handshake to include version %q, got %q", version, handshake)
	}
}

type stubRepo struct {
	cutoff time.Time
	stats  sqlite.CleanupStats
	err    error
	calls  int
}

func (s *stubRepo) RunRetentionCleanup(cutoff time.Time) (sqlite.CleanupStats, error) {
	s.calls++
	s.cutoff = cutoff
	return s.stats, s.err
}

func TestRunStartupRetentionCleanup_UsesNowMinusSevenDaysUTC(t *testing.T) {
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.FixedZone("EST", -5*3600))
	expectedCutoff := time.Date(2026, 4, 11, 17, 0, 0, 0, time.UTC)

	stub := &stubRepo{
		stats: sqlite.CleanupStats{DeletedErrorEvents: 3, DeletedScanSessions: 2, DeletedPlans: 1},
	}

	err := runStartupRetentionCleanup(stub, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 call, got %d", stub.calls)
	}
	if !stub.cutoff.Equal(expectedCutoff) {
		t.Fatalf("expected cutoff %v, got %v", expectedCutoff, stub.cutoff)
	}
}

func TestRunStartupRetentionCleanup_PropagatesRepoError(t *testing.T) {
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	stub := &stubRepo{err: errTestCleanup}

	err := runStartupRetentionCleanup(stub, now)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != errTestCleanup {
		t.Fatalf("expected errTestCleanup, got %v", err)
	}
}

var errTestCleanup = errors.New("test cleanup error")
