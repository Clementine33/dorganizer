package library_test

import (
	"testing"

	"github.com/onsei/organizer/backend/internal/library"
)

// TestMatchDirIDRefusesAnAmbiguousIdentity pins the rule the API cannot reach:
// the inventory cannot hold two rows for one path, so an identity two
// directories claim is refused where the match is decided, never answered with
// the first of them (ADR 0003 §4).
func TestMatchDirIDRefusesAnAmbiguousIdentity(t *testing.T) {
	identity := library.DirID("lib-1", "/music", "albumA")

	rel, found, ambiguous := library.MatchDirID([]string{"albumA", "albumA"}, "lib-1", "/music", identity)
	if found || !ambiguous || rel != "" {
		t.Fatalf("an ambiguous identity resolved to %q (found=%v ambiguous=%v)", rel, found, ambiguous)
	}
	if rel, found, ambiguous = library.MatchDirID(
		[]string{"other", "albumA"}, "lib-1", "/music", identity,
	); !found || ambiguous || rel != "albumA" {
		t.Fatalf("match = %q / %v / %v, want albumA", rel, found, ambiguous)
	}
	if _, found, ambiguous = library.MatchDirID([]string{"other"}, "lib-1", "/music", identity); found || ambiguous {
		t.Fatal("an absent identity was reported as found")
	}
	// The library is part of the identity: the same path in another library is
	// another directory.
	if _, found, _ = library.MatchDirID([]string{"albumA"}, "lib-2", "/music", identity); found {
		t.Fatal("another library's directory answered to this identity")
	}
}
