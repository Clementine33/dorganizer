package grpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func TestPlanOperations_Prune_PersistsPlanType(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-prune-*")
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

	// Create test audio files (both mp3 and flac for prune target)
	testFile1 := filepath.Join(tmpDir, "test1.mp3")
	testFile2 := filepath.Join(tmpDir, "test1.flac")
	if err := os.WriteFile(testFile1, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(testFile2, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Insert entries into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile1), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(testFile2), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create config.json with valid prune regex pattern
	configJSON := `{
		"prune": {
			"regex_pattern": "test"
		}
	}`
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call PlanOperations with prune target (scoped to tmpDir so require_scope is satisfied)
	req := &pb.PlanOperationsRequest{
		PlanType:     "prune",
		TargetFormat: "prune:both",
		FolderPath:   tmpDir,
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify plan_id is returned
	if resp.PlanId == "" {
		t.Error("plan_id should not be empty")
	}

	// Verify plan_type in database is "prune"
	plan, err := repo.GetPlan(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}
	if plan.PlanType != "prune" {
		t.Errorf("expected plan_type 'prune', got %q", plan.PlanType)
	}
}

// TestPlanOperations_Prune_UsesConfigRegexPattern verifies that prune operation
// reads the regex_pattern from config.json and passes it to AnalyzePrune.
// This test uses two stem pairs - only one matching the regex - to prove
// the config regex is actually being used (not just passing with empty pattern).
func TestPlanOperations_Prune_UsesConfigRegexPattern(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-prune-regex-*")
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

	// Create test audio files with two stems.
	//
	// Stem pair 1: matches regex "keep_"
	// - keep_song.mp3 (lossy)
	// - keep_song.flac (lossless)
	//
	// Stem pair 2: does NOT match regex "keep_"
	// - remove_track.mp3 (lossy)
	// - remove_track.flac (lossless)
	//
	// With regex "keep_" and prune:both, both keep_song files should be pruned.
	// An empty pattern would prune both stems (4 operations), proving regex is wired.
	keepSongMP3 := filepath.Join(tmpDir, "keep_song.mp3")
	keepSongFLAC := filepath.Join(tmpDir, "keep_song.flac")
	removeTrackMP3 := filepath.Join(tmpDir, "remove_track.mp3")
	removeTrackFLAC := filepath.Join(tmpDir, "remove_track.flac")

	files := []string{keepSongMP3, keepSongFLAC, removeTrackMP3, removeTrackFLAC}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("dummy audio"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Insert entries into DB
	entries := []struct {
		path   string
		format string
	}{
		{keepSongMP3, "audio/mpeg"},
		{keepSongFLAC, "audio/flac"},
		{removeTrackMP3, "audio/mpeg"},
		{removeTrackFLAC, "audio/flac"},
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

	// Create config.json with prune.regex_pattern "keep_"
	// This means only files with "keep_" prefix should be prune candidates
	configJSON := `{
		"prune": {
			"regex_pattern": "keep_"
		}
	}`
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create server with config dir
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call PlanOperations with prune target (scoped to tmpDir so require_scope is satisfied)
	req := &pb.PlanOperationsRequest{
		PlanType:     "prune",
		TargetFormat: "prune:both",
		FolderPath:   tmpDir,
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify plan_id is returned
	if resp.PlanId == "" {
		t.Error("plan_id should not be empty")
	}

	// Get plan items
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	// With regex_pattern "keep_", only the keep_song pair should be prune candidates.
	// The remove_track pair does NOT match "keep_" so should NOT be pruned.
	// With prune:both, both matched audio types should be pruned.
	// Total expected: 2 prune operations (keep_song.mp3 + keep_song.flac).
	// If regex was empty/ignored, we'd get 4 operations.
	expectedCount := 2
	if len(items) != expectedCount {
		t.Errorf("expected %d prune operations (only keep_song pair), got %d", expectedCount, len(items))
	}

	// Verify each operation path matches keep_song and none is remove_track.
	for _, item := range items {
		actualPath := item.SourcePath
		if actualPath != filepath.ToSlash(keepSongMP3) && actualPath != filepath.ToSlash(keepSongFLAC) {
			t.Errorf("expected prune operation on %q or %q, got %q",
				filepath.ToSlash(keepSongMP3), filepath.ToSlash(keepSongFLAC), actualPath)
		}
		if strings.Contains(actualPath, "remove_track") {
			t.Errorf("prune operation should NOT be on remove_track (doesn't match regex 'keep_'), got %q", actualPath)
		}
		t.Logf("  Prune operation on: %s (matches regex 'keep_')", actualPath)
	}
}

// TestPlanOperations_Prune_FailsOnInvalidConfig verifies that prune planning
// fails safely when config is missing, invalid JSON, or has empty regex pattern.
func TestPlanOperations_Prune_FailsOnInvalidConfig(t *testing.T) {
	tests := []struct {
		name       string
		configJSON string
		wantErr    bool
	}{
		{
			name:       "missing config file",
			configJSON: "", // no config.json created
			wantErr:    true,
		},
		{
			name:       "invalid JSON",
			configJSON: "{invalid json}",
			wantErr:    true,
		},
		{
			name:       "empty regex pattern",
			configJSON: `{"prune": {"regex_pattern": ""}}`,
			wantErr:    true,
		},
		{
			name:       "malformed regex pattern",
			configJSON: `{"prune": {"regex_pattern": "["}}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory for test
			tmpDir, err := os.MkdirTemp("", "onsei-test-prune-fail-*")
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

			// Create test audio file
			testFile := filepath.Join(tmpDir, "test.mp3")
			if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			// Insert entry into DB
			_, err = repo.DB().Exec(`
				INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
				VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
			`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), 1234567890)
			if err != nil {
				t.Fatalf("failed to insert entry: %v", err)
			}

			// Create config.json if provided
			if tt.configJSON != "" {
				configPath := filepath.Join(tmpDir, "config.json")
				if err := os.WriteFile(configPath, []byte(tt.configJSON), 0644); err != nil {
					t.Fatalf("failed to write config: %v", err)
				}
			}

			// Create server
			server := NewOnseiServer(repo, tmpDir, "ffmpeg")

			// Call PlanOperations with prune target (scoped to tmpDir so require_scope is satisfied)
			req := &pb.PlanOperationsRequest{
				PlanType:     "prune",
				TargetFormat: "prune:both",
				FolderPath:   tmpDir,
			}
			_, err = server.PlanOperations(nil, req)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
