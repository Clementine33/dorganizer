package main

import (
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/app"
)

func TestVersion_DefaultNonEmptyAndEmittedInHandshake(t *testing.T) {
	if strings.TrimSpace(version) == "" {
		t.Fatal("expected default version to be non-empty")
	}

	handshake := app.BuildHandshakeLine("token-1", version, 54321)
	if !strings.Contains(handshake, "version="+version) {
		t.Fatalf("expected handshake to include version %q, got %q", version, handshake)
	}
	if !strings.Contains(handshake, "http_port=54321") {
		t.Fatalf("expected handshake to include http_port, got %q", handshake)
	}
}
