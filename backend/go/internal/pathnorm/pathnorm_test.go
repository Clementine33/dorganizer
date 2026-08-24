package pathnorm

import (
	"path/filepath"
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
