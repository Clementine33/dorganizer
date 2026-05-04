package analyze

import "testing"

func TestMode2Branch_AnyLossyBelowThreshold(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.wav"}),
		toFileEntry(Entry{PathPosix: "/music/album/song.mp3", Bitrate: 192000}),
	}}

	branch := DetermineBranch(buildStemGroups(comp), true)
	if branch.BranchType != BranchConvert {
		t.Fatalf("expected BranchConvert, got %v", branch.BranchType)
	}
}

func TestMode2Branch_AllLossyAboveThreshold(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.wav"}),
		toFileEntry(Entry{PathPosix: "/music/album/song.mp3", Bitrate: 320000}),
	}}

	branch := DetermineBranch(buildStemGroups(comp), true)
	if branch.BranchType != BranchDeleteLossless {
		t.Fatalf("expected BranchDeleteLossless, got %v", branch.BranchType)
	}
}

func TestMode2Branch_NoLossy(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.wav"}),
	}}

	branch := DetermineBranch(buildStemGroups(comp), true)
	if branch.BranchType != BranchConvert {
		t.Fatalf("expected BranchConvert, got %v", branch.BranchType)
	}
}

func TestMode2Branch_UnknownBitrate(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.wav"}),
		toFileEntry(Entry{PathPosix: "/music/album/song.mp3", Bitrate: 0}),
	}}

	branch := DetermineBranch(buildStemGroups(comp), true)
	if branch.BranchType != BranchUnknown {
		t.Fatalf("expected BranchUnknown, got %v", branch.BranchType)
	}
}

func TestMode2Branch_LossyOnlyBelowThreshold_Skip(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.mp3", Bitrate: 192000}),
	}}

	branch := DetermineBranch(buildStemGroups(comp), true)
	if branch.BranchType != BranchSkip {
		t.Fatalf("expected BranchSkip, got %v", branch.BranchType)
	}
}

func TestMode2Branch_LossyOnlyUnknownBitrate_Skip(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.mp3", Bitrate: 0}),
	}}

	branch := DetermineBranch(buildStemGroups(comp), true)
	if branch.BranchType != BranchSkip {
		t.Fatalf("expected BranchSkip, got %v", branch.BranchType)
	}
}
