package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFiltersAllowedAudioExtensions(t *testing.T) {
	// Create temp dir with wav/flac/mp3 + txt/lrc files
	tmpDir := t.TempDir()

	// Create supported audio files
	audioFiles := []string{"song.wav", "song.flac", "song.mp3", "song.aac", "song.m4a"}
	for _, f := range audioFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	}

	// Create unsupported files
	unsupportedFiles := []string{"lyrics.txt", "lyrics.lrc", "readme.md", "cover.jpg"}
	for _, f := range unsupportedFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	}

	// Create a subdirectory with more files
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.mp3"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create nested file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create nested file: %v", err)
	}

	// Scan the directory
	entries, err := ScanDirectory(tmpDir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	// Expect only supported audio files
	expectedCount := len(audioFiles) + 1 // +1 for nested.mp3
	if len(entries) != expectedCount {
		t.Errorf("expected %d entries, got %d", expectedCount, len(entries))
	}

	// Verify all entries have allowed extensions
	allowedExts := map[string]bool{
		".wav":  true,
		".flac": true,
		".mp3":  true,
		".aac":  true,
		".m4a":  true,
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Path)
		if !allowedExts[ext] {
			t.Errorf("found unsupported extension: %s in path %s", ext, e.Path)
		}
	}
}

func TestScanCollectsFileInfo(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.mp3")
	content := []byte("dummy audio content")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	entries, err := ScanDirectory(tmpDir)
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].FileSize != int64(len(content)) {
		t.Errorf("expected FileSize=%d, got %d", len(content), entries[0].FileSize)
	}
}
