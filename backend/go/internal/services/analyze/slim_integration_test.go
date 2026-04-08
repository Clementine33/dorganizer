package analyze

import (
	"regexp"
	"testing"
)

func TestSlimMode2Integration_CrossParentAll320(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/album/a/1-foo.wav"},
		{PathPosix: "/music/album/a/2-foo.wav"},
		{PathPosix: "/music/album/a/3-foo.wav"},
		{PathPosix: "/music/album/b/1-foo.mp3", Bitrate: 320000},
		{PathPosix: "/music/album/b/2-foo.mp3", Bitrate: 320000},
		{PathPosix: "/music/album/b/3-foo.mp3", Bitrate: 320000},
	}

	plan := AnalyzeSlimMode2(entries, nil)
	var deletes, converts int
	for _, op := range plan.Operations {
		if op.Type == OpTypeDelete {
			deletes++
		}
		if op.Type == OpTypeConvert {
			converts++
		}
	}
	if deletes != 3 || converts != 0 {
		t.Fatalf("expected 3 deletes and 0 converts, got %d deletes and %d converts", deletes, converts)
	}
}

func TestSlimMode2Integration_IndependentComponents(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/album/a/1-foo.wav"},
		{PathPosix: "/music/album/b/1-foo.mp3", Bitrate: 320000},
		{PathPosix: "/music/album/c/2-bar.wav"},
		{PathPosix: "/music/album/d/2-bar.mp3", Bitrate: 192000},
	}

	plan := AnalyzeSlimMode2(entries, nil)
	var deletes, converts int
	for _, op := range plan.Operations {
		if op.Type == OpTypeDelete {
			deletes++
		}
		if op.Type == OpTypeConvert {
			converts++
		}
	}

	if deletes != 2 || converts != 1 {
		t.Fatalf("expected 2 deletes and 1 convert, got %d deletes and %d converts", deletes, converts)
	}
}

func TestSlimMode2Integration_UnknownMp3AddsWarning(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/album/a/song.wav"},
		{PathPosix: "/music/album/b/song.mp3", Bitrate: 0},
	}

	plan := AnalyzeSlimMode2(entries, nil)
	if len(plan.Errors) == 0 {
		t.Fatalf("expected warning for skipped component")
	}
	if plan.Errors[0].Code != "SLIM_GROUP_SKIPPED_BITRATE_UNKNOWN" {
		t.Fatalf("unexpected warning code: %s", plan.Errors[0].Code)
	}
}

func TestSlimMode2Integration_PrunePatternBeforeBuildComponents(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/album/a/song.wav"},
		{PathPosix: "/music/album/a/song.mp3", Bitrate: 320000},
	}

	planNoPrune := AnalyzeSlimMode2(entries, nil)
	hasWavDeleteWithoutPrune := false
	for _, op := range planNoPrune.Operations {
		if op.SourcePath == "/music/album/a/song.wav" {
			hasWavDeleteWithoutPrune = true
			break
		}
	}
	if !hasWavDeleteWithoutPrune {
		t.Fatalf("expected plan without prune pre-filter to include wav operation")
	}

	purePattern := mustCompileRegexp(t, `\.wav$`)
	planWithPrune := AnalyzeSlimMode2(entries, purePattern)
	for _, op := range planWithPrune.Operations {
		if op.SourcePath == "/music/album/a/song.wav" {
			t.Fatalf("expected wav entry to be pruned before build components, found operation %+v", op)
		}
	}
}

func mustCompileRegexp(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("compile regexp %q: %v", pattern, err)
	}
	return re
}
