package grpc

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPlanOperations_SummaryFields_EmptyCase tests that when no files match
// the planning criteria, the summary counters reflect: all zeros.
// TestPlanOperations_Attribution_ValidPath tests attribution of a valid path inside root.
func TestPlanOperations_Attribution_ValidPath(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantFolder string
	}{
		{
			name:       "first level child",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Album/song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "nested path still maps to first level child",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Album/Disc1/song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "trailing slash on root",
			rootPath:   "C:/Music/",
			candidate:  "C:/Music/Artist/song.mp3",
			wantFolder: "C:/Music/Artist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if got != tt.wantFolder {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.candidate, got, tt.wantFolder)
			}
		})
	}
}

// TestPlanOperations_Attribution_EscapeAndOutside tests that escape attempts and outside-root paths map to global.

// TestPlanOperations_Attribution_EscapeAndOutside tests that escape attempts and outside-root paths map to global.
func TestPlanOperations_Attribution_EscapeAndOutside(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantGlobal bool // true means folder_path should be ""
	}{
		{
			name:       "parent traversal escapes root",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Album/../../../Windows/System32",
			wantGlobal: true,
		},
		{
			name:       "absolute path outside root",
			rootPath:   "C:/Music",
			candidate:  "D:/Other/song.mp3",
			wantGlobal: true,
		},
		{
			name:       "empty candidate",
			rootPath:   "C:/Music",
			candidate:  "",
			wantGlobal: true,
		},
		{
			name:       "root itself returns empty (no first level child)",
			rootPath:   "C:/Music",
			candidate:  "C:/Music",
			wantGlobal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if tt.wantGlobal && got != "" {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want empty string for global", tt.rootPath, tt.candidate, got)
			}
		})
	}
}

// TestPlanOperations_Attribution_Normalization tests path normalization behavior.

// TestPlanOperations_Attribution_Normalization tests path normalization behavior.
func TestPlanOperations_Attribution_Normalization(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantFolder string
	}{
		{
			name:       "backslash converted to forward slash",
			rootPath:   "C:\\Music",
			candidate:  "C:\\Music\\Album\\song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "mixed separators normalized",
			rootPath:   "C:/Music",
			candidate:  "C:\\Music/Album\\song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "double slashes collapsed",
			rootPath:   "C:/Music",
			candidate:  "C://Music//Album/song.mp3",
			wantFolder: "C:/Music/Album",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			// Normalize expected for comparison (handles platform differences)
			wantNorm := filepath.ToSlash(filepath.Clean(tt.wantFolder))
			if got != wantNorm {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.candidate, got, wantNorm)
			}
		})
	}
}

// TestPlanOperations_Attribution_WindowsCaseInsensitive tests case-insensitive matching on Windows.

// TestPlanOperations_Attribution_WindowsCaseInsensitive tests case-insensitive matching on Windows.
func TestPlanOperations_Attribution_WindowsCaseInsensitive(t *testing.T) {
	// On Windows, case-insensitive matching should work
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive attribution test only relevant on Windows")
	}

	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantFolder string
	}{
		{
			name:       "different case on root",
			rootPath:   "C:/music",
			candidate:  "C:/MUSIC/Album/song.mp3",
			wantFolder: "C:/MUSIC/Album",
		},
		{
			name:       "different case on folder",
			rootPath:   "C:/Music",
			candidate:  "C:/music/ALBUM/song.mp3",
			wantFolder: "C:/music/ALBUM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			wantNorm := filepath.ToSlash(filepath.Clean(tt.wantFolder))
			if got != wantNorm {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.candidate, got, wantNorm)
			}
		})
	}
}

// TestPlanOperations_Attribution_NonWindowsCaseSensitive tests that on non-Windows
// systems, attribution is case-sensitive (different case = different path = not contained).

// TestPlanOperations_Attribution_NonWindowsCaseSensitive tests that on non-Windows
// systems, attribution is case-sensitive (different case = different path = not contained).
func TestPlanOperations_Attribution_NonWindowsCaseSensitive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("case-sensitive attribution test only relevant on non-Windows")
	}

	tests := []struct {
		name      string
		rootPath  string
		candidate string
		wantEmpty bool // true means should return "" (case mismatch on non-Windows)
	}{
		{
			name:      "different case on root - case-sensitive mismatch",
			rootPath:  "/music",
			candidate: "/MUSIC/Album/song.mp3",
			wantEmpty: true, // /MUSIC is different from /music on case-sensitive systems
		},
		{
			name:      "different case on folder - case-sensitive mismatch",
			rootPath:  "/Music",
			candidate: "/music/ALBUM/song.mp3",
			wantEmpty: true, // /music is different from /Music on case-sensitive systems
		},
		{
			name:      "exact case match - should work",
			rootPath:  "/Music",
			candidate: "/Music/Album/song.mp3",
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if tt.wantEmpty && got != "" {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want empty string (case-sensitive mismatch on non-Windows)",
					tt.rootPath, tt.candidate, got)
			}
			if !tt.wantEmpty && got == "" {
				t.Errorf("attributeFolderPath(%q, %q) = empty, want non-empty (exact case match)",
					tt.rootPath, tt.candidate)
			}
		})
	}
}

// TestPlanOperations_Attribution_UnreliablePathFallback tests that lexically
// ambiguous or unreliable paths map to global fallback ("").
// This explicitly tests the spec requirement: "when path normalization fails
// or produces unreliable results, attribution should fall back to global".

// TestPlanOperations_Attribution_UnreliablePathFallback tests that lexically
// ambiguous or unreliable paths map to global fallback ("").
// This explicitly tests the spec requirement: "when path normalization fails
// or produces unreliable results, attribution should fall back to global".
func TestPlanOperations_Attribution_UnreliablePathFallback(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantGlobal bool // true means folder_path should be "" (global fallback)
		reason     string
	}{
		{
			name:       "path with null bytes is unreliable",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Al\u0000bum/song.mp3",
			wantGlobal: true,
			reason:     "path contains null bytes which are invalid/unreliable",
		},
		{
			name:       "excessively long path is unreliable",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/" + strings.Repeat("a", 500) + "/song.mp3",
			wantGlobal: false, // long but valid paths should still work
			reason:     "long paths should still be processed",
		},
		{
			name:       "path with only dots as segment",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/.../song.mp3",
			wantGlobal: false, // dots-only segments are valid (though unusual)
			reason:     "dots-only segments are still valid path components",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if tt.wantGlobal && got != "" {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want empty string (global fallback). Reason: %s",
					tt.rootPath, tt.candidate, got, tt.reason)
			}
			if !tt.wantGlobal && got == "" {
				t.Errorf("attributeFolderPath(%q, %q) = empty, want non-empty. Reason: %s",
					tt.rootPath, tt.candidate, tt.reason)
			}
		})
	}
}

// TestPlanOperations_SummaryFields_EmptyCase tests that when no files match
// the planning criteria, the summary counters reflect: all zeros.
