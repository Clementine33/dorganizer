package analyze

import "testing"

func TestGenerateOperations_BranchA(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.wav"}),
		toFileEntry(Entry{PathPosix: "/music/album/song.mp3", Bitrate: 192000}),
		toFileEntry(Entry{PathPosix: "/music/album/song.m4a", Bitrate: 256000}),
	}}

	ops := GenerateOperations(comp, BranchResult{BranchType: BranchConvert, SourcePath: "/music/album/song.wav", Reason: "test"})
	var converts, deletes int
	for _, op := range ops {
		if op.Type == OpTypeConvert {
			converts++
		}
		if op.Type == OpTypeDelete {
			deletes++
		}
	}
	if converts != 1 || deletes != 2 {
		t.Fatalf("expected 1 convert + 2 deletes, got %d convert + %d deletes", converts, deletes)
	}
}

func TestGenerateOperations_BranchB(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.wav"}),
		toFileEntry(Entry{PathPosix: "/music/album/song.mp3", Bitrate: 320000}),
	}}

	ops := GenerateOperations(comp, BranchResult{BranchType: BranchDeleteLossless, Reason: "test"})
	if len(ops) != 1 || ops[0].Type != OpTypeDelete || ops[0].SourcePath != "/music/album/song.wav" {
		t.Fatalf("expected only wav delete, got %+v", ops)
	}
}

func TestGenerateOperations_BranchDeleteLossless_ConvertsUnpairedLossless(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/a/0.wav"}),
		toFileEntry(Entry{PathPosix: "/music/a/1.wav"}),
		toFileEntry(Entry{PathPosix: "/music/b/1.mp3", Bitrate: 320000}),
	}}

	ops := GenerateOperations(comp, BranchResult{BranchType: BranchDeleteLossless, Reason: "test"})

	var delete1Wav, convert0Wav, delete0Wav bool
	for _, op := range ops {
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
		t.Fatalf("expected delete for paired lossless stem, got %+v", ops)
	}
	if !convert0Wav {
		t.Fatalf("expected convert for unpaired lossless stem, got %+v", ops)
	}
	if delete0Wav {
		t.Fatalf("did not expect delete for unpaired lossless source, got %+v", ops)
	}
}

func TestGenerateOperations_BranchC_WavPreferred(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song.flac"}),
		toFileEntry(Entry{PathPosix: "/music/album/song.wav"}),
	}}

	branch := DetermineBranch(comp)
	ops := GenerateOperations(comp, branch)
	if len(ops) == 0 || ops[0].Type != OpTypeConvert || ops[0].SourcePath != "/music/album/song.wav" {
		t.Fatalf("expected wav convert op, got %+v", ops)
	}
}

func TestGenerateOperations_BranchConvert_DeletesOnlyMatchedStemLossy(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/album/song1.wav"}),
		toFileEntry(Entry{PathPosix: "/music/album/song1.mp3", Bitrate: 192000}),
		toFileEntry(Entry{PathPosix: "/music/album/song2.mp3", Bitrate: 192000}),
	}}

	ops := GenerateOperations(comp, BranchResult{BranchType: BranchConvert, Reason: "test"})

	var convertSong1, deleteSong1, deleteSong2 bool
	for _, op := range ops {
		if op.Type == OpTypeConvert && op.SourcePath == "/music/album/song1.wav" {
			convertSong1 = true
		}
		if op.Type == OpTypeDelete && op.SourcePath == "/music/album/song1.mp3" {
			deleteSong1 = true
		}
		if op.Type == OpTypeDelete && op.SourcePath == "/music/album/song2.mp3" {
			deleteSong2 = true
		}
	}

	if !convertSong1 || !deleteSong1 {
		t.Fatalf("expected convert song1.wav and delete song1.mp3, got %+v", ops)
	}
	if deleteSong2 {
		t.Fatalf("expected song2.mp3 to be kept because no same-stem lossless source, got %+v", ops)
	}
}
