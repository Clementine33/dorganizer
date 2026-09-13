package main

import (
	"context"
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

	handshake := bootstrap.BuildHandshakeLine(43123, "token-1", version, 54321)
	if !strings.Contains(handshake, "version="+version) {
		t.Fatalf("expected handshake to include version %q, got %q", version, handshake)
	}
	if !strings.Contains(handshake, "http_port=54321") {
		t.Fatalf("expected handshake to include http_port, got %q", handshake)
	}
}

type stubRepo struct {
	cutoff           time.Time
	generationCutoff time.Time
	stats            sqlite.CleanupStats
	err              error
	calls            int
}

func (s *stubRepo) RunRetentionCleanupWithCutoffs(cutoff, generationCutoff time.Time) (sqlite.CleanupStats, error) {
	s.calls++
	s.cutoff = cutoff
	s.generationCutoff = generationCutoff
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
	if !errors.Is(err, errTestCleanup) {
		t.Fatalf("expected errTestCleanup, got %v", err)
	}
}

func TestParseCORSOrigins(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "empty falls back to defaults",
			raw:  "",
			want: []string{"http://localhost:5173", "http://127.0.0.1:5173"},
		},
		{
			name: "custom comma-separated with whitespace",
			raw:  " http://a.test ,http://b.test , ,http://c.test ",
			want: []string{"http://a.test", "http://b.test", "http://c.test"},
		},
		{
			name: "single origin",
			raw:  "http://localhost:8080",
			want: []string{"http://localhost:8080"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCORSOrigins(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v want %v", got, tt.want)
				}
			}
		})
	}
}

var errTestCleanup = errors.New("test cleanup error")

// TestDrainServersStartsBothAndUnblocksOnDeadline verifies the shutdown
// coordinator starts HTTP and gRPC drains concurrently (gRPC must not wait for
// the HTTP drain to finish first) and force-stops both once the graceful
// deadline arrives, so the drain returns.
func TestDrainServersStartsBothAndUnblocksOnDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	httpEntered := make(chan struct{}, 1)
	grpcEntered := make(chan struct{}, 1)
	httpClosed := make(chan struct{}, 1)
	grpcStopped := make(chan struct{}, 1)

	drainServers(ctx,
		func(c context.Context) error {
			httpEntered <- struct{}{}
			<-c.Done() // HTTP drain blocks past the caller's request
			return c.Err()
		},
		func() error {
			httpClosed <- struct{}{}
			return nil
		},
		func() {
			grpcEntered <- struct{}{}
			<-grpcStopped // gRPC graceful stop blocks until force-stopped
		},
		func() {
			close(grpcStopped)
		},
	)

	select {
	case <-httpEntered:
	default:
		t.Error("http shutdown never entered")
	}
	select {
	case <-grpcEntered:
	default:
		t.Error("gRPC graceful stop never entered concurrently")
	}
	select {
	case <-httpClosed:
	default:
		t.Error("http force-close never ran at the deadline")
	}
}
