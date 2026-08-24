package main

import (
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/bootstrap"
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
