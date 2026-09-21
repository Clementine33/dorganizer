package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/bootstrap"
)

func TestVersion_DefaultNonEmptyAndEmittedInHandshake(t *testing.T) {
	if strings.TrimSpace(version) == "" {
		t.Fatal("expected default version to be non-empty")
	}

	handshake := bootstrap.BuildHandshakeLine("token-1", version, 54321)
	if !strings.Contains(handshake, "version="+version) {
		t.Fatalf("expected handshake to include version %q, got %q", version, handshake)
	}
	if !strings.Contains(handshake, "http_port=54321") {
		t.Fatalf("expected handshake to include http_port, got %q", handshake)
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

// TestDrainHTTPServerUnblocksOnDeadline verifies the shutdown coordinator
// starts the HTTP drain and force-closes it once the graceful deadline
// arrives, so the drain always returns.
func TestDrainHTTPServerUnblocksOnDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	httpEntered := make(chan struct{}, 1)
	httpClosed := make(chan struct{}, 1)

	drainHTTPServer(ctx,
		func(c context.Context) error {
			httpEntered <- struct{}{}
			<-c.Done() // the drain blocks past the caller's request
			return c.Err()
		},
		func() error {
			httpClosed <- struct{}{}
			return nil
		},
	)

	select {
	case <-httpEntered:
	default:
		t.Error("http shutdown never entered")
	}
	select {
	case <-httpClosed:
	default:
		t.Error("http force-close never ran at the deadline")
	}
}
