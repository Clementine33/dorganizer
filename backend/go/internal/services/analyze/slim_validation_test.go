package analyze

import "testing"

func TestBuildStemGroups_SortsByStemAndPreservesFileOrder(t *testing.T) {
	comp := Component{FileEntries: []FileEntry{
		toFileEntry(Entry{PathPosix: "/music/z/song2.mp3", Bitrate: 320000}),
		toFileEntry(Entry{PathPosix: "/music/a/song1.wav"}),
		toFileEntry(Entry{PathPosix: "/music/b/song1.mp3", Bitrate: 320000}),
	}}

	groups := buildStemGroups(comp)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].Stem != "song1" || groups[1].Stem != "song2" {
		t.Fatalf("unexpected group order: %+v", groups)
	}
	if groups[0].Files[0].PathPosix != "/music/a/song1.wav" || groups[0].Files[1].PathPosix != "/music/b/song1.mp3" {
		t.Fatalf("expected file order preserved inside song1 group, got %+v", groups[0].Files)
	}
}

func TestValidateStemGroups_Rules(t *testing.T) {
	tests := []struct {
		name  string
		files []Entry
		want  []string
	}{
		{
			name: "pure lossy repeated mp3 allowed",
			files: []Entry{
				{PathPosix: "/music/a/song.mp3", Bitrate: 320000},
				{PathPosix: "/music/b/song.mp3", Bitrate: 320000},
				{PathPosix: "/music/c/song.mp3", Bitrate: 320000},
			},
		},
		{
			name: "pure lossy mixed formats rejected",
			files: []Entry{
				{PathPosix: "/music/a/song.mp3", Bitrate: 320000},
				{PathPosix: "/music/b/song.m4a", Bitrate: 256000},
			},
			want: []string{"SLIM_STEM_LOSSY_MIXED_FORMATS"},
		},
		{
			name: "one lossless with multiple lossy rejected",
			files: []Entry{
				{PathPosix: "/music/a/song.wav"},
				{PathPosix: "/music/b/song.mp3", Bitrate: 320000},
				{PathPosix: "/music/c/song.mp3", Bitrate: 320000},
			},
			want: []string{"SLIM_STEM_LOSSLESS_WITH_MULTI_LOSSY"},
		},
		{
			name: "multiple lossless rejected",
			files: []Entry{
				{PathPosix: "/music/a/song.wav"},
				{PathPosix: "/music/b/song.flac"},
				{PathPosix: "/music/c/song.mp3", Bitrate: 320000},
			},
			want: []string{"SLIM_STEM_MULTI_LOSSLESS"},
		},
		{
			name: "single lossless stem in mixed component allowed",
			files: []Entry{
				{PathPosix: "/music/a/song.wav"},
				{PathPosix: "/music/a/other.wav"},
				{PathPosix: "/music/b/song.mp3", Bitrate: 320000},
			},
		},
		{
			name: "pure lossless component has no shared stem error",
			files: []Entry{
				{PathPosix: "/music/a/song.wav"},
			},
		},
		{
			name: "strict one plus one allowed",
			files: []Entry{
				{PathPosix: "/music/a/song.wav"},
				{PathPosix: "/music/b/song.mp3", Bitrate: 320000},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := BuildComponents(tt.files)[0]
			groups := buildStemGroups(comp)
			errs := validateStemGroups(comp, groups)

			if len(errs) != len(tt.want) {
				t.Fatalf("expected %d errors, got %+v", len(tt.want), errs)
			}
			for i, wantCode := range tt.want {
				if errs[i].Code != wantCode {
					t.Fatalf("expected error %q at index %d, got %+v", wantCode, i, errs)
				}
			}
		})
	}
}
