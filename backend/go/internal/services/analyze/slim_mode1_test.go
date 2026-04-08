package analyze

import (
	"regexp"
	"testing"
)

func TestSlimMode1_UsesMode2GroupingAcrossParents(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/a/song.wav"},
		{PathPosix: "/music/b/song.mp3", Bitrate: 0},
	}

	plan := AnalyzeSlimMode1(entries, nil)
	if len(plan.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", plan.Errors)
	}

	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	if plan.Operations[0].Type != OpTypeDelete || plan.Operations[0].SourcePath != "/music/a/song.wav" {
		t.Fatalf("expected delete /music/a/song.wav, got %+v", plan.Operations[0])
	}
}

func TestSlimMode1_LossyOnlyGroupSkipped(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/a/song1.mp3", Bitrate: 128000},
		{PathPosix: "/music/a/song2.m4a", Bitrate: 256000},
	}

	plan := AnalyzeSlimMode1(entries, nil)
	if len(plan.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", plan.Errors)
	}
	if len(plan.Operations) != 0 {
		t.Fatalf("expected no operations for lossy-only group, got %+v", plan.Operations)
	}
}

func TestSlimMode1_LosslessOnlyGroupErrors(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/a/song.wav"},
		{PathPosix: "/music/a/song.flac"},
	}

	plan := AnalyzeSlimMode1(entries, nil)
	if len(plan.Errors) == 0 {
		t.Fatal("expected error for lossless-only group")
	}
	if plan.Errors[0].Code != "SLIM_MODE1_LOSSLESS_ONLY" {
		t.Fatalf("expected SLIM_MODE1_LOSSLESS_ONLY, got %s", plan.Errors[0].Code)
	}
}

func TestSlimMode1_StemGreaterThanTwoErrors(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/a/song.wav"},
		{PathPosix: "/music/a/song.flac"},
		{PathPosix: "/music/a/song.mp3", Bitrate: 320000},
	}

	plan := AnalyzeSlimMode1(entries, nil)
	if len(plan.Errors) == 0 {
		t.Fatal("expected error for stem match > 2")
	}
	if plan.Errors[0].Code != "SLIM_STEM_MATCH_GT2" {
		t.Fatalf("expected SLIM_STEM_MATCH_GT2, got %s", plan.Errors[0].Code)
	}
}

func TestSlimMode1_IgnoresBitrateCheckAndKeepsLossy(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/a/song.wav"},
		{PathPosix: "/music/b/song.mp3", Bitrate: 0},
	}

	plan := AnalyzeSlimMode1(entries, nil)
	if len(plan.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", plan.Errors)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected one delete operation, got %+v", plan.Operations)
	}
	if plan.Operations[0].Type != OpTypeDelete || plan.Operations[0].SourcePath != "/music/a/song.wav" {
		t.Fatalf("unexpected operation: %+v", plan.Operations[0])
	}
}

func TestSlimMode1_PreservesCompletenessByConvertingUnpairedLossless(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/a/0.wav"},
		{PathPosix: "/music/a/1.wav"},
		{PathPosix: "/music/b/1.mp3", Bitrate: 320000},
	}

	plan := AnalyzeSlimMode1(entries, nil)
	if len(plan.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", plan.Errors)
	}

	var delete1Wav, convert0Wav, delete0Wav bool
	for _, op := range plan.Operations {
		if op.Type == OpTypeDelete && op.SourcePath == "/music/a/1.wav" {
			delete1Wav = true
		}
		if op.Type == OpTypeConvert && op.SourcePath == "/music/a/0.wav" && op.TargetPath == "/music/a/0.m4a" {
			convert0Wav = true
		}
		if op.Type == OpTypeDelete && op.SourcePath == "/music/a/0.wav" {
			delete0Wav = true
		}
	}

	if !delete1Wav {
		t.Fatalf("expected delete for paired lossless stem, got %+v", plan.Operations)
	}
	if !convert0Wav {
		t.Fatalf("expected convert for unpaired lossless stem, got %+v", plan.Operations)
	}
	if delete0Wav {
		t.Fatalf("did not expect delete for unpaired lossless source, got %+v", plan.Operations)
	}
}

func TestSlimMode1_PruneMatchedExcluded_FiltersBeforeBuildComponents(t *testing.T) {
	entries := []Entry{
		{PathPosix: "/music/a/song.wav"},
		{PathPosix: "/music/a/song.mp3", Bitrate: 320000},
	}

	planNoExclude := AnalyzeSlimMode1(entries, nil)
	hasWavOperationWithoutExclude := false
	for _, op := range planNoExclude.Operations {
		if op.SourcePath == "/music/a/song.wav" {
			hasWavOperationWithoutExclude = true
			break
		}
	}
	if !hasWavOperationWithoutExclude {
		t.Fatalf("expected plan without matched-entry exclusion to include wav operation")
	}

	purePattern := mustCompileMode1Regexp(t, `\.wav$`)
	planWithExclude := AnalyzeSlimMode1(entries, purePattern)
	for _, op := range planWithExclude.Operations {
		if op.SourcePath == "/music/a/song.wav" {
			t.Fatalf("expected wav entry to be excluded before build components, found operation %+v", op)
		}
	}
}

func mustCompileMode1Regexp(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("compile regexp %q: %v", pattern, err)
	}
	return re
}
