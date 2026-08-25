package pathnorm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeToPOSIX(t *testing.T) {
	got := NormalizeToPOSIX(`C:\music\A\B.mp3`)
	if got != "C:/music/A/B.mp3" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanRootPathAndRootPathKey(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantClean string
		wantKey   string
	}{
		{name: "posix trailing", in: "/music/", wantClean: "/music", wantKey: "/music"},
		{name: "posix dot", in: "/music/.", wantClean: "/music", wantKey: "/music"},
		{name: "posix repeated separators", in: "/music//A//", wantClean: "/music/A", wantKey: "/music/A"},
		{name: "posix root", in: "/", wantClean: "/", wantKey: "/"},
		{name: "posix case preserved", in: "/Music", wantClean: "/Music", wantKey: "/Music"},
		{name: "posix backslash", in: `\music\x`, wantClean: "/music/x", wantKey: "/music/x"},
		{name: "windows drive preserves case display", in: `C:\Music\`, wantClean: "C:/Music", wantKey: "c:/music"},
		{name: "windows drive lower", in: "c:/music", wantClean: "c:/music", wantKey: "c:/music"},
		{name: "windows drive root", in: "C:/", wantClean: "C:/", wantKey: "c:/"},
		{name: "windows drive case variant collides", in: `C:\MUSIC\Album`, wantClean: "C:/MUSIC/Album", wantKey: "c:/music/album"},
		{name: "unc case folded", in: `\\SERVER\Share\Dir\..\`, wantClean: "//SERVER/Share", wantKey: "//server/share"},
		{name: "device path", in: `\\?\C:\music`, wantClean: `//?/C:/music`, wantKey: `//?/c:/music`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanRootPath(tt.in); got != tt.wantClean {
				t.Errorf("CleanRootPath(%q) = %q, want %q", tt.in, got, tt.wantClean)
			}
			if got := RootPathKey(tt.in); got != tt.wantKey {
				t.Errorf("RootPathKey(%q) = %q, want %q", tt.in, got, tt.wantKey)
			}
		})
	}
}

func TestIsWindowsUNCPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "standard unc", path: `\\server\share\music`, want: true},
		{name: "long unc", path: `\\?\UNC\server\share\music`, want: true},
		{name: "long local drive", path: `\\?\C:\\music`, want: false},
		{name: "drive path", path: `C:\\music`, want: false},
		{name: "slash unc", path: `//server/share/music`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWindowsUNCPath(tt.path)
			if got != tt.want {
				t.Fatalf("IsWindowsUNCPath(%q)=%v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsWithinRoot(t *testing.T) {
	tests := []struct {
		name      string
		root      string
		candidate string
		want      bool
	}{
		{name: "same POSIX path", root: "/music", candidate: "/music", want: true},
		{name: "POSIX descendant", root: "/music", candidate: "/music/album/track.flac", want: true},
		{name: "rejects sibling prefix", root: "/music", candidate: "/music-other/track.flac", want: false},
		{name: "cleans parent traversal", root: "/music", candidate: "/music/album/../track.flac", want: true},
		{name: "rejects escaped traversal", root: "/music", candidate: "/music/../outside.flac", want: false},
		{name: "Windows separators and case", root: `C:\Music`, candidate: `c:\music\Album\track.flac`, want: true},
		{name: "same Windows drive root", root: `C:\`, candidate: `c:/`, want: true},
		{name: "rejects another Windows drive", root: `C:\Music`, candidate: `D:\Music\track.flac`, want: false},
		{name: "UNC comparison is case insensitive", root: `\\Server\Share\Music`, candidate: `\\server\share\music\track.flac`, want: true},
		{name: "empty candidate", root: "/music", candidate: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWithinRoot(tt.root, tt.candidate); got != tt.want {
				t.Fatalf("IsWithinRoot(%q, %q) = %v, want %v", tt.root, tt.candidate, got, tt.want)
			}
		})
	}
}

func TestIsResolvedWithinRoot(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "library")
	insideDir := filepath.Join(root, "album")
	outsideDir := filepath.Join(workspace, "outside")
	if err := os.MkdirAll(insideDir, 0o755); err != nil {
		t.Fatalf("create inside directory: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	insideFile := filepath.Join(insideDir, "inside.flac")
	outsideFile := filepath.Join(outsideDir, "outside.flac")
	if err := os.WriteFile(insideFile, []byte("audio"), 0o644); err != nil {
		t.Fatalf("create inside file: %v", err)
	}
	if err := os.WriteFile(outsideFile, []byte("audio"), 0o644); err != nil {
		t.Fatalf("create outside file: %v", err)
	}

	within, err := IsResolvedWithinRoot(root, insideFile)
	if err != nil {
		t.Fatalf("IsResolvedWithinRoot inside file: %v", err)
	}
	if !within {
		t.Fatal("inside file should resolve within root")
	}

	// On Windows the filesystem is case-insensitive but filepath.EvalSymlinks
	// does not canonicalize letter case, so a case-swapped inside path must
	// still resolve within the root (mirrors IsWithinRoot's case folding).
	if runtime.GOOS == "windows" {
		caseSwapped := filepath.Join(filepath.Dir(insideDir), strings.ToUpper(filepath.Base(insideDir)), filepath.Base(insideFile))
		within, err = IsResolvedWithinRoot(root, caseSwapped)
		if err != nil {
			t.Fatalf("IsResolvedWithinRoot case-swapped inside file: %v", err)
		}
		if !within {
			t.Fatal("case-swapped inside file should resolve within root on Windows")
		}
	}

	linkPath := filepath.Join(root, "linked")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	within, err = IsResolvedWithinRoot(root, filepath.Join(linkPath, filepath.Base(outsideFile)))
	if err != nil {
		t.Fatalf("IsResolvedWithinRoot symlink escape: %v", err)
	}
	if within {
		t.Fatal("file reached through an escaping symlink should not resolve within root")
	}
}

func TestTruncatePathComponentsToBytes_UTF8Boundary(t *testing.T) {
	longComponent := strings.Repeat("单", 120) // 360 bytes in UTF-8
	if len(longComponent) <= 214 {
		t.Fatalf("test setup invalid: component bytes=%d, need >214", len(longComponent))
	}

	input := filepath.Join("1-单一", "12_一般", longComponent)
	got := TruncatePathComponentsToBytes(input, 214)

	parts := strings.Split(got, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if len(part) > 214 {
			t.Fatalf("component bytes=%d exceeds 214: %q", len(part), part)
		}
		if !utf8.ValidString(part) {
			t.Fatalf("component is not valid UTF-8: %q", part)
		}
	}
}
