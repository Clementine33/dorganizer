package analyze

import (
	"regexp"
	"testing"
)

func TestPruneMatchesByPattern(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/temp/test.flac"},
		{PathPosix: "/music/keep/song.mp3"},
		{PathPosix: "/music/backup/old.flac"},
	}

	// Pattern to match temp/ and backup/ directories
	pattern := regexp.MustCompile(`/(temp|backup)/`)

	matches := MatchByPattern(entries, pattern, "temp/backup")

	if len(matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matches))
	}
}

func TestPruneGeneratesDeleteOperations(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/keep/song.mp3"},
	}

	pattern := regexp.MustCompile(`/backup/`)

	matches := MatchByPattern(entries, pattern, "backup")
	plan := GeneratePrunePlan(matches, "backup")

	if len(plan.Operations) != 1 {
		t.Errorf("expected 1 delete operation, got %d", len(plan.Operations))
	}

	if plan.Operations[0].Type != OpTypeDelete {
		t.Errorf("expected delete operation, got %v", plan.Operations[0].Type)
	}
}

func TestAnalyzePrune_LossyTarget(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/backup/song.flac"},
		{PathPosix: "/music/keep/song.mp3"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossy)

	// Should have no errors (no counterpart requirement)
	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	// Should have one delete operation for the mp3 in backup
	if len(ops) != 1 {
		t.Errorf("expected 1 operation, got %d", len(ops))
		return
	}

	if ops[0].SourcePath != "/music/backup/song.mp3" {
		t.Errorf("expected to delete /music/backup/song.mp3, got %s", ops[0].SourcePath)
	}
}

func TestAnalyzePrune_NoCounterpartRequired(t *testing.T) {
	// Lossy files without counterpart are deleted without error
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossy)

	// No error - counterpart not required
	if len(errors) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errors), errors)
	}

	// Should delete the file
	if len(ops) != 1 {
		t.Errorf("expected 1 operation, got %d", len(ops))
		return
	}

	if ops[0].SourcePath != "/music/backup/song.mp3" {
		t.Errorf("expected to delete /music/backup/song.mp3, got %s", ops[0].SourcePath)
	}
}

func TestAnalyzePrune_SiblingFolderMatch(t *testing.T) {
	// Pattern matches only backup/, not backup2/
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/backup2/song.flac"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossy)

	// No errors
	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	// Should match only backup/
	if len(ops) != 1 {
		t.Errorf("expected 1 operation for backup folder, got %d", len(ops))
		return
	}

	if ops[0].SourcePath != "/music/backup/song.mp3" {
		t.Errorf("expected to delete /music/backup/song.mp3, got %s", ops[0].SourcePath)
	}
}

func TestAnalyzePrune_MultipleStemsInFolder(t *testing.T) {
	// Each matching file is deleted independently
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/backup/song.flac"},
		{PathPosix: "/music/backup/track.mp3"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossy)

	// No errors
	if len(errors) != 0 {
		t.Errorf("expected no errors, got %d errors: %v", len(errors), errors)
	}

	// Should delete both lossy files
	if len(ops) != 2 {
		t.Errorf("expected 2 operations for lossy files, got %d", len(ops))
		return
	}
}

func TestAnalyzePrune_DescendantFolderMatch(t *testing.T) {
	// Pattern matches backup and all descendants
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/backup/subfolder/song.flac"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossy)

	// No errors
	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	// Should match only the mp3 (lossy target)
	if len(ops) != 1 {
		t.Errorf("expected 1 operation, got %d", len(ops))
		return
	}

	if ops[0].SourcePath != "/music/backup/song.mp3" {
		t.Errorf("expected to delete /music/backup/song.mp3, got %s", ops[0].SourcePath)
	}
}

func TestAnalyzePrune_PruneTargetBoth(t *testing.T) {
	// For PruneTargetBoth: both lossy and lossless files are deleted
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetBoth)

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	if len(ops) != 1 {
		t.Errorf("expected 1 operation, got %d", len(ops))
		return
	}

	if ops[0].SourcePath != "/music/backup/song.mp3" {
		t.Errorf("expected to delete /music/backup/song.mp3, got %s", ops[0].SourcePath)
	}
}

func TestAnalyzePrune_PruneTargetBothDeletesBoth(t *testing.T) {
	// For PruneTargetBoth: both file types are deleted
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/backup/song.flac"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetBoth)

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	// Should delete both files
	if len(ops) != 2 {
		t.Errorf("expected 2 operations, got %d", len(ops))
		return
	}
}

func TestAnalyzePrune_PruneTargetBothDescendantFolder(t *testing.T) {
	// For PruneTargetBoth: deletes matching audio files in subtree
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
		{PathPosix: "/music/backup/subfolder/song.flac"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetBoth)

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	// Should delete both matching files
	if len(ops) != 2 {
		t.Errorf("expected 2 operations, got %d", len(ops))
		return
	}
}

func TestAnalyzePrune_LosslessTarget(t *testing.T) {
	// Lossless target deletes lossless files without counterpart requirement
	entries := []Entry{
		{PathPosix: "/music/backup/song.flac"},
		{PathPosix: "/music/backup/song.mp3"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossless)

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	if len(ops) != 1 {
		t.Errorf("expected 1 operation, got %d", len(ops))
		return
	}

	if ops[0].SourcePath != "/music/backup/song.flac" {
		t.Errorf("expected to delete /music/backup/song.flac, got %s", ops[0].SourcePath)
	}
}

func TestAnalyzePrune_LosslessTargetNoCounterpart(t *testing.T) {
	// Lossless file without counterpart is deleted without error
	entries := []Entry{
		{PathPosix: "/music/backup/song.flac"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossless)

	// No error - counterpart not required
	if len(errors) != 0 {
		t.Errorf("expected no errors, got %d: %v", len(errors), errors)
	}

	// Should delete the file
	if len(ops) != 1 {
		t.Errorf("expected 1 operation, got %d", len(ops))
		return
	}

	if ops[0].SourcePath != "/music/backup/song.flac" {
		t.Errorf("expected to delete /music/backup/song.flac, got %s", ops[0].SourcePath)
	}
}

func TestAnalyzePrune_InvalidPattern(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/backup/song.mp3"},
	}

	ops, errors := AnalyzePrune(entries, `[invalid(`, PruneTargetLossy)

	if len(errors) != 1 {
		t.Errorf("expected 1 error for invalid pattern, got %d", len(errors))
		return
	}

	if errors[0].Code != "INVALID_PATTERN" {
		t.Errorf("expected error code INVALID_PATTERN, got %s", errors[0].Code)
	}

	if ops != nil {
		t.Errorf("expected nil ops for invalid pattern, got %v", ops)
	}
}

func TestAnalyzePrune_EmptyEntries(t *testing.T) {
	ops, errors := AnalyzePrune([]Entry{}, `/backup/`, PruneTargetLossy)

	if len(errors) != 0 {
		t.Errorf("expected no errors for empty entries, got %v", errors)
	}

	if len(ops) != 0 {
		t.Errorf("expected 0 operations for empty entries, got %d", len(ops))
	}
}

func TestAnalyzePrune_NoPatternMatch(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/keep/song.mp3"},
		{PathPosix: "/music/keep/song.flac"},
	}

	ops, errors := AnalyzePrune(entries, `/backup/`, PruneTargetLossy)

	if len(errors) != 0 {
		t.Errorf("expected no errors, got %v", errors)
	}

	if len(ops) != 0 {
		t.Errorf("expected 0 operations (no pattern match), got %d", len(ops))
	}
}

// TestAnalyzePrune_BehaviorParity_AfterAllocationRefactor verifies that the
// refactored AnalyzePrune produces consistent output for the same inputs.
// This test validates the simplified pattern-only matching semantics.
func TestAnalyzePrune_BehaviorParity_AfterAllocationRefactor(t *testing.T) {
	testCases := []struct {
		name            string
		entries         []Entry
		pattern         string
		target          PruneTarget
		expectedOps     int
		expectedErrors  int
		checkFirstOp    string // expected first op path (if ops expected)
		checkFirstError string // expected first error code (if errors expected)
	}{
		{
			name:           "empty entries",
			entries:        []Entry{},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    0,
			expectedErrors: 0,
		},
		{
			name: "lossy with lossless in same folder - both match",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
				{PathPosix: "/music/backup/song.flac"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.mp3",
		},
		{
			name: "lossy without counterpart - still deleted",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.mp3",
		},
		{
			name: "sibling folder does not affect pattern match",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
				{PathPosix: "/music/backup2/song.flac"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.mp3",
		},
		{
			name: "descendant folder matches pattern",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
				{PathPosix: "/music/backup/sub/song.flac"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.mp3",
		},
		{
			name: "prune target both deletes both types",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
				{PathPosix: "/music/backup/song.flac"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetBoth,
			expectedOps:    2,
			expectedErrors: 0,
		},
		{
			name: "prune target both deletes standalone lossy",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetBoth,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.mp3",
		},
		{
			name: "prune target both deletes standalone lossless",
			entries: []Entry{
				{PathPosix: "/music/backup/song.flac"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetBoth,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.flac",
		},
		{
			name: "lossless target deletes lossless",
			entries: []Entry{
				{PathPosix: "/music/backup/song.flac"},
				{PathPosix: "/music/backup/song.mp3"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossless,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.flac",
		},
		{
			name: "lossless target without counterpart - still deleted",
			entries: []Entry{
				{PathPosix: "/music/backup/song.flac"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossless,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.flac",
		},
		{
			name: "multiple stems deleted independently",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
				{PathPosix: "/music/backup/song.flac"},
				{PathPosix: "/music/backup/track.mp3"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    2,
			expectedErrors: 0,
		},
		{
			name: "invalid pattern returns error",
			entries: []Entry{
				{PathPosix: "/music/backup/song.mp3"},
			},
			pattern:         `[invalid(`,
			target:          PruneTargetLossy,
			expectedOps:     0,
			expectedErrors:  1,
			checkFirstError: "INVALID_PATTERN",
		},
		{
			name: "case insensitive extension matching",
			entries: []Entry{
				{PathPosix: "/music/backup/song.MP3"},
				{PathPosix: "/music/backup/song.FLAC"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.MP3",
		},
		{
			name: "non-audio files ignored",
			entries: []Entry{
				{PathPosix: "/music/backup/cover.jpg"},
				{PathPosix: "/music/backup/song.mp3"},
			},
			pattern:        `/backup/`,
			target:         PruneTargetLossy,
			expectedOps:    1,
			expectedErrors: 0,
			checkFirstOp:   "/music/backup/song.mp3",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ops, errs := AnalyzePrune(tc.entries, tc.pattern, tc.target)

			// Check expected counts
			if len(ops) != tc.expectedOps {
				t.Errorf("expected %d operations, got %d", tc.expectedOps, len(ops))
			}
			if len(errs) != tc.expectedErrors {
				t.Errorf("expected %d errors, got %d: %v", tc.expectedErrors, len(errs), errs)
			}

			// Check first op path if specified
			if tc.checkFirstOp != "" && len(ops) > 0 {
				if ops[0].SourcePath != tc.checkFirstOp {
					t.Errorf("expected first op path %s, got %s", tc.checkFirstOp, ops[0].SourcePath)
				}
			}

			// Check first error code if specified
			if tc.checkFirstError != "" && len(errs) > 0 {
				if errs[0].Code != tc.checkFirstError {
					t.Errorf("expected first error code %s, got %s", tc.checkFirstError, errs[0].Code)
				}
			}

			// Verify consistency: same input should always produce same output
			for i := 0; i < 3; i++ {
				ops2, errs2 := AnalyzePrune(tc.entries, tc.pattern, tc.target)

				if len(ops) != len(ops2) || len(errs) != len(errs2) {
					t.Errorf("iteration %d: result count mismatch", i)
					continue
				}

				for j := range ops {
					if ops[j] != ops2[j] {
						t.Errorf("iteration %d: op %d mismatch", i, j)
					}
				}

				for j := range errs {
					if errs[j] != errs2[j] {
						t.Errorf("iteration %d: err %d mismatch", i, j)
					}
				}
			}

			// Validate operation structure
			for _, op := range ops {
				if op.Type != OpTypeDelete {
					t.Errorf("expected OpTypeDelete, got %v", op.Type)
				}
				if op.Reason != "PRUNE_MATCH" {
					t.Errorf("expected Reason='PRUNE_MATCH', got %q", op.Reason)
				}
				if op.SourcePath == "" {
					t.Error("expected non-empty SourcePath")
				}
			}
		})
	}
}

// BenchmarkAnalyzePrune benchmarks the AnalyzePrune function to measure
// allocation improvements after refactoring.
func BenchmarkAnalyzePrune(b *testing.B) {
	entries := make([]Entry, 1000)
	for i := 0; i < 1000; i++ {
		entries[i] = Entry{PathPosix: "/music/backup/folder/file" + string(rune('0'+i%10)) + ".mp3"}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		AnalyzePrune(entries, `/backup/`, PruneTargetBoth)
	}
}
