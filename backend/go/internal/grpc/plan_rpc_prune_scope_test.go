package grpc

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestPlanOperations_Prune_RespectsFolderPathScope verifies prune planning
// only includes entries under the requested folder_path scope.
func TestPlanOperations_Prune_RespectsFolderPathScope(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-prune-scope-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	folderA := filepath.Join(tmpDir, "A")
	folderB := filepath.Join(tmpDir, "B")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatalf("failed to create folder A: %v", err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatalf("failed to create folder B: %v", err)
	}

	// Both folders contain prune-match files with same stem pair.
	aMP3 := filepath.Join(folderA, "keep_song.mp3")
	aFLAC := filepath.Join(folderA, "keep_song.flac")
	bMP3 := filepath.Join(folderB, "keep_song.mp3")
	bFLAC := filepath.Join(folderB, "keep_song.flac")

	files := []string{aMP3, aFLAC, bMP3, bFLAC}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("dummy audio"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	entries := []struct {
		path   string
		format string
	}{
		{aMP3, "audio/mpeg"},
		{aFLAC, "audio/flac"},
		{bMP3, "audio/mpeg"},
		{bFLAC, "audio/flac"},
	}
	for _, e := range entries {
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, ?, 1, ?)
		`, filepath.ToSlash(e.path), filepath.ToSlash(tmpDir), e.format, 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}
	}

	configJSON := `{
		"prune": {
			"regex_pattern": "keep_"
		}
	}`
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		PlanType:     "prune",
		TargetFormat: "prune:both",
		FolderPath:   filepath.ToSlash(folderA),
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	// Expect only folderA entries (2 operations), not folderB.
	if len(items) != 2 {
		t.Fatalf("expected 2 prune operations under folderA scope, got %d", len(items))
	}

	for _, item := range items {
		if !strings.HasPrefix(filepath.ToSlash(item.SourcePath), filepath.ToSlash(folderA)+"/") {
			t.Errorf("expected scoped prune path under %q, got %q", filepath.ToSlash(folderA), item.SourcePath)
		}
	}
}

// TestPlanOperations_Prune_MixedScopes_UnionDedup verifies that folder_path,
// folder_paths, and source_files are combined as a union with proper deduplication.
func TestPlanOperations_Prune_MixedScopes_UnionDedup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-prune-mixed-scopes-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Create folder structure
	folderA := filepath.Join(tmpDir, "A")
	folderB := filepath.Join(tmpDir, "B")
	folderC := filepath.Join(tmpDir, "C")
	for _, f := range []string{folderA, folderB, folderC} {
		if err := os.MkdirAll(f, 0755); err != nil {
			t.Fatalf("failed to create folder %s: %v", f, err)
		}
	}

	// Create files:
	// - a.mp3 in folderA (via folder_path)
	// - b.mp3 in folderB (via folder_paths)
	// - c.mp3 in folderC (via source_files, but folderC also in folder_paths - should dedup)
	aMP3 := filepath.Join(folderA, "prune_a.mp3")
	bMP3 := filepath.Join(folderB, "prune_b.mp3")
	cMP3 := filepath.Join(folderC, "prune_c.mp3")
	dMP3 := filepath.Join(folderC, "prune_d.mp3") // Also in folderC, should be included

	files := []string{aMP3, bMP3, cMP3, dMP3}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", f, err)
		}
	}

	// Insert entries
	entries := []struct {
		path   string
		format string
	}{
		{aMP3, "audio/mpeg"},
		{bMP3, "audio/mpeg"},
		{cMP3, "audio/mpeg"},
		{dMP3, "audio/mpeg"},
	}
	for _, e := range entries {
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, ?, 1, ?)
		`, filepath.ToSlash(e.path), filepath.ToSlash(tmpDir), e.format, 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}
	}

	configJSON := `{"prune": {"regex_pattern": "prune_"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Mix of folder_path, folder_paths, and source_files
	req := &pb.PlanOperationsRequest{
		PlanType:     "prune",
		TargetFormat: "prune:mp3aac",             // prune lossy files
		FolderPath:   folderA,                    // Single folder
		FolderPaths:  []string{folderB, folderC}, // Multiple folders (folderC overlaps with source_files)
		SourceFiles:  []string{cMP3},             // Specific file (should be deduped with folderC contents)
	}

	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	// Should have 4 unique operations (a, b, c, d - all pruned as lossy mp3)
	// c should not be duplicated even though it's in both folderC and source_files
	if len(items) != 4 {
		t.Fatalf("expected 4 unique prune operations (deduped), got %d", len(items))
	}

	// Verify all expected files are present
	foundPaths := make(map[string]bool)
	for _, item := range items {
		foundPaths[item.SourcePath] = true
	}

	expectedPaths := []string{
		filepath.ToSlash(aMP3),
		filepath.ToSlash(bMP3),
		filepath.ToSlash(cMP3),
		filepath.ToSlash(dMP3),
	}

	for _, expected := range expectedPaths {
		if !foundPaths[expected] {
			t.Errorf("expected prune operation for %s not found", expected)
		}
	}
}

// TestPlanOperations_Prune_ScopeEscaping_PercentUnderscore verifies that % and _
// characters in folder paths are escaped in LIKE patterns so they match literally.
func TestPlanOperations_Prune_ScopeEscaping_PercentUnderscore(t *testing.T) {
	t.Run("percent", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "onsei-test-prune-escape-pct-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		dbPath := filepath.Join(tmpDir, "test.db")
		repo, err := sqlite.NewRepository(dbPath)
		if err != nil {
			t.Fatalf("failed to create repo: %v", err)
		}
		defer repo.Close()

		// Create folder with % in name
		folderPercent := filepath.Join(tmpDir, "Music_100%_quality")
		if err := os.MkdirAll(folderPercent, 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}
		filePercent := filepath.Join(folderPercent, "song.mp3")
		if err := os.WriteFile(filePercent, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		// Insert entry
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(filePercent), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		// Also insert a file that would match if % was interpreted as wildcard
		otherFolder := filepath.Join(tmpDir, "MusicXquality")
		if err := os.MkdirAll(otherFolder, 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}
		otherFile := filepath.Join(otherFolder, "other.mp3")
		if err := os.WriteFile(otherFile, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(otherFile), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		configJSON := `{"prune": {"regex_pattern": "song"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		server := NewOnseiServer(repo, tmpDir, "ffmpeg")

		// Request only the folder with % in its name
		req := &pb.PlanOperationsRequest{
			PlanType:     "prune",
			TargetFormat: "prune:mp3aac",
			FolderPath:   folderPercent,
		}

		resp, err := server.PlanOperations(nil, req)
		if err != nil {
			t.Fatalf("PlanOperations failed: %v", err)
		}

		items, err := repo.ListPlanItems(resp.PlanId)
		if err != nil {
			t.Fatalf("failed to list plan items: %v", err)
		}

		// Should only get the file from Music_100%_quality, NOT from MusicXquality
		if len(items) != 1 {
			t.Fatalf("expected 1 prune operation (only from exact folder match), got %d", len(items))
		}

		if items[0].SourcePath != filepath.ToSlash(filePercent) {
			t.Errorf("expected file from %s, got %s", folderPercent, items[0].SourcePath)
		}
	})

	t.Run("underscore", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "onsei-test-prune-escape-und-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		dbPath := filepath.Join(tmpDir, "test.db")
		repo, err := sqlite.NewRepository(dbPath)
		if err != nil {
			t.Fatalf("failed to create repo: %v", err)
		}
		defer repo.Close()

		// Create folder with _ in name
		folderUnderscore := filepath.Join(tmpDir, "Music_test_file")
		if err := os.MkdirAll(folderUnderscore, 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}
		fileUnderscore := filepath.Join(folderUnderscore, "song.mp3")
		if err := os.WriteFile(fileUnderscore, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		// Insert entry
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(fileUnderscore), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		// Create a folder where underscore would match if it were a wildcard
		// 'X' would match '_' if _ were wildcard
		folderWithPatternMatch := filepath.Join(tmpDir, "MusicXtest_file")
		if err := os.MkdirAll(folderWithPatternMatch, 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}
		fileInPatternFolder := filepath.Join(folderWithPatternMatch, "song.mp3")
		if err := os.WriteFile(fileInPatternFolder, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(fileInPatternFolder), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		configJSON := `{"prune": {"regex_pattern": "song"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		server := NewOnseiServer(repo, tmpDir, "ffmpeg")

		// Request the folder with underscore in its name
		req := &pb.PlanOperationsRequest{
			PlanType:     "prune",
			TargetFormat: "prune:mp3aac",
			FolderPath:   folderUnderscore,
		}

		resp, err := server.PlanOperations(nil, req)
		if err != nil {
			t.Fatalf("PlanOperations failed: %v", err)
		}

		items, err := repo.ListPlanItems(resp.PlanId)
		if err != nil {
			t.Fatalf("failed to list plan items: %v", err)
		}

		// Should only get the file from Music_test_file (with underscore), NOT from MusicXtest_file
		// If _ were a wildcard, Music_test_file would match MusicXtest_file and we'd get both
		if len(items) != 1 {
			t.Fatalf("expected 1 prune operation from underscore folder, got %d (underscore may not be escaped)", len(items))
		}

		if items[0].SourcePath != filepath.ToSlash(fileUnderscore) {
			t.Errorf("expected file from %s, got %s", folderUnderscore, items[0].SourcePath)
		}
	})
}

// TestPlanOperations_Prune_RootScope_NoDoubleSlashLike verifies that when the
// scope is root ("/"), the LIKE parameter is "/%" not "//%".
func TestPlanOperations_Prune_RootScope_NoDoubleSlashLike(t *testing.T) {
	// Skip this test on Windows as root "/" is not a valid path
	if runtime.GOOS == "windows" {
		t.Skip("Root scope '/' test not applicable on Windows")
	}

	tmpDir, err := os.MkdirTemp("", "onsei-test-prune-root-like-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Create entries with absolute paths under root
	// The entries table stores paths like "/home/user/music/song.mp3"
	testFile := "/tmp/test_prune_root/song.mp3"

	// Insert entry with absolute path under root
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, testFile, "/tmp/test_prune_root", 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	configJSON := `{"prune": {"regex_pattern": "song"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Test with actual root scope "/" - this should produce "/%" not "//%"
	req := &pb.PlanOperationsRequest{
		PlanType:     "prune",
		TargetFormat: "prune:mp3aac",
		FolderPath:   "/",
	}

	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	// Should successfully find the file (not fail due to malformed LIKE pattern "//%")
	if len(items) != 1 {
		t.Fatalf("expected 1 prune operation, got %d", len(items))
	}

	// Verify the file path matches what we expect from root scope query
	if items[0].SourcePath != testFile {
		t.Errorf("expected source path %q, got %q", testFile, items[0].SourcePath)
	}
}

// TestPlanOperations_Prune_EmptyScopes_NoInEmptyClause verifies that empty
// folder_paths or source_files don't generate invalid SQL like "IN ()".
func TestPlanOperations_Prune_EmptyScopes_NoInEmptyClause(t *testing.T) {
	t.Run("source_files_only", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "onsei-test-prune-empty-src-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		dbPath := filepath.Join(tmpDir, "test.db")
		repo, err := sqlite.NewRepository(dbPath)
		if err != nil {
			t.Fatalf("failed to create repo: %v", err)
		}
		defer repo.Close()

		testFile := filepath.Join(tmpDir, "song.mp3")
		if err := os.WriteFile(testFile, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		configJSON := `{"prune": {"regex_pattern": "song"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		server := NewOnseiServer(repo, tmpDir, "ffmpeg")

		// Test with only source_files (empty folder_paths implicitly)
		req := &pb.PlanOperationsRequest{
			PlanType:     "prune",
			TargetFormat: "prune:mp3aac",
			SourceFiles:  []string{testFile},
		}

		resp, err := server.PlanOperations(nil, req)
		if err != nil {
			t.Fatalf("PlanOperations with source_files only failed: %v", err)
		}

		items, err := repo.ListPlanItems(resp.PlanId)
		if err != nil {
			t.Fatalf("failed to list plan items: %v", err)
		}

		if len(items) != 1 {
			t.Fatalf("expected 1 prune operation from source_files, got %d", len(items))
		}
	})

	t.Run("empty_folder_paths_slice", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "onsei-test-prune-empty-fld-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		dbPath := filepath.Join(tmpDir, "test.db")
		repo, err := sqlite.NewRepository(dbPath)
		if err != nil {
			t.Fatalf("failed to create repo: %v", err)
		}
		defer repo.Close()

		testFile := filepath.Join(tmpDir, "song.mp3")
		if err := os.WriteFile(testFile, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		configJSON := `{"prune": {"regex_pattern": "song"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		server := NewOnseiServer(repo, tmpDir, "ffmpeg")

		// Test with explicit empty folder_paths slice
		req := &pb.PlanOperationsRequest{
			PlanType:     "prune",
			TargetFormat: "prune:mp3aac",
			FolderPaths:  []string{}, // Explicitly empty
			SourceFiles:  []string{testFile},
		}

		resp, err := server.PlanOperations(nil, req)
		if err != nil {
			t.Fatalf("PlanOperations with empty folder_paths failed: %v", err)
		}

		items, err := repo.ListPlanItems(resp.PlanId)
		if err != nil {
			t.Fatalf("failed to list plan items: %v", err)
		}

		if len(items) != 1 {
			t.Fatalf("expected 1 prune operation, got %d", len(items))
		}
	})
}

// TestPlanOperations_Prune_LargeScopes_ChunkedQuery verifies that large numbers
// of scopes are handled via chunked queries to avoid SQL parameter limits.
func TestPlanOperations_Prune_LargeScopes_ChunkedQuery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-prune-chunked-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Create enough folders to exceed SQLite parameter limit (999)
	// Each folder uses 2 parameters (path = ? AND path LIKE ?), so we need > 500 folders
	// Using 600 folders ensures we trigger chunking (chunkSize is 400 folders = 800 params)
	numFolders := 600
	expectedFiles := make(map[string]bool)

	for i := 0; i < numFolders; i++ {
		folder := filepath.Join(tmpDir, fmt.Sprintf("folder_%04d", i))
		if err := os.MkdirAll(folder, 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}

		// Create a file in each folder
		file := filepath.Join(folder, "song.mp3")
		if err := os.WriteFile(file, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(file), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		expectedFiles[filepath.ToSlash(file)] = true
	}

	configJSON := `{"prune": {"regex_pattern": "song"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Create folder paths array that exceeds chunk size (600 folders > 400 chunk size)
	// This ensures chunking behavior is actually exercised
	folderPaths := make([]string, numFolders)
	for i := 0; i < numFolders; i++ {
		folderPaths[i] = filepath.Join(tmpDir, fmt.Sprintf("folder_%04d", i))
	}

	req := &pb.PlanOperationsRequest{
		PlanType:     "prune",
		TargetFormat: "prune:mp3aac",
		FolderPaths:  folderPaths,
	}

	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed with large scope: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	// Should have all files from all folders
	if len(items) != numFolders {
		t.Fatalf("expected %d prune operations (one per folder), got %d", numFolders, len(items))
	}

	// Verify all expected files are present
	foundFiles := make(map[string]bool)
	for _, item := range items {
		foundFiles[item.SourcePath] = true
	}

	for expected := range expectedFiles {
		if !foundFiles[expected] {
			t.Errorf("expected file %s not found in results", expected)
		}
	}

	t.Logf("Successfully processed %d folders with chunked queries", numFolders)
}
