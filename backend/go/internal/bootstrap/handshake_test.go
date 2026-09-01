package bootstrap_test

import (
	"testing"

	"github.com/onsei/organizer/backend/internal/bootstrap"
)

func TestBuildHandshakeLine(t *testing.T) {
	got := bootstrap.BuildHandshakeLine(51234, "tok-1", "v1", 54321)
	want := "ONSEI_BACKEND_READY port=51234 token=tok-1 version=v1 http_port=54321"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
