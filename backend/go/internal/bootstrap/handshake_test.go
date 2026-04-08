package bootstrap

import "testing"

func TestBuildHandshakeLine(t *testing.T) {
	got := BuildHandshakeLine(51234, "tok-1", "v1")
	want := "ONSEI_BACKEND_READY port=51234 token=tok-1 version=v1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
