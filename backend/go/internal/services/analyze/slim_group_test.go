package analyze

import "testing"

func TestBuildComponents_FromSelectedFiles(t *testing.T) {
	files := []Entry{
		{PathPosix: "/music/album1/song.wav"},
		{PathPosix: "/music/album2/song.mp3", Bitrate: 320000},
	}

	components := BuildComponents(files)
	if len(components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(components))
	}
	if len(components[0].FileEntries) != 2 {
		t.Fatalf("expected component with 2 files, got %d", len(components[0].FileEntries))
	}
}

func TestBuildComponents_IndependentFolders(t *testing.T) {
	files := []Entry{
		{PathPosix: "/music/album1/track1.wav"},
		{PathPosix: "/music/album2/track2.wav"},
	}

	components := BuildComponents(files)
	if len(components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(components))
	}
}

func TestBuildComponents_ConnectedGraphByFolderAndStem(t *testing.T) {
	files := []Entry{
		{PathPosix: "/music/a/1.wav"},
		{PathPosix: "/music/a/2.wav"},
		{PathPosix: "/music/b/2.mp3", Bitrate: 320000},
	}

	components := BuildComponents(files)
	if len(components) != 1 {
		t.Fatalf("expected 1 connected component, got %d", len(components))
	}
	if len(components[0].FileEntries) != 3 {
		t.Fatalf("expected 3 files in component, got %d", len(components[0].FileEntries))
	}
}

func TestBuildComponents_ExcludesSidecars(t *testing.T) {
	files := []Entry{
		{PathPosix: "/music/a/song.wav"},
		{PathPosix: "/music/a/song.lrc"},
		{PathPosix: "/music/a/song.vtt"},
	}

	components := BuildComponents(files)
	if len(components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(components))
	}
	if len(components[0].FileEntries) != 1 {
		t.Fatalf("expected sidecars excluded, got %d files", len(components[0].FileEntries))
	}
}
