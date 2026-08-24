package grpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func TestPlanOperations_Slim_PersistsPlanType(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-*")
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

	// Create test audio files
	testFile1 := filepath.Join(tmpDir, "test1.mp3")
	testFile2 := filepath.Join(tmpDir, "test2.flac")
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

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call PlanOperations with folder_path (slim mode)
	req := &pb.PlanOperationsRequest{
		FolderPath: tmpDir,
		PlanType:   "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify plan_id is returned
	if resp.PlanId == "" {
		t.Error("plan_id should not be empty")
	}

	// Verify plan_type in database is "slim"
	plan, err := repo.GetPlan(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}
	if plan.PlanType != "slim" {
		t.Errorf("expected plan_type 'slim', got %q", plan.PlanType)
	}
}

func TestPlanOperations_ResponseOrderMatchesPersistedPlanItems(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-plan-order-align-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	scanRoot := filepath.Join(tmpDir, "scan-root")
	folder1 := filepath.Join(scanRoot, "A", "folder1")
	folder2 := filepath.Join(scanRoot, "A", "folder2")
	folder10 := filepath.Join(scanRoot, "A", "folder10")

	for _, tc := range []struct {
		folder string
		file   string
	}{
		{folder: folder10, file: "track10.flac"},
		{folder: folder2, file: "track2.flac"},
		{folder: folder1, file: "track1.flac"},
	} {
		fullPath := filepath.Join(tc.folder, tc.file)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("dummy flac"), 0644); err != nil {
			t.Fatalf("failed to write fixture file: %v", err)
		}

		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
		`, filepath.ToSlash(fullPath), filepath.ToSlash(scanRoot), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:    "slim",
		FolderPaths: []string{folder10, folder2, folder1},
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	ops := resp.GetOperations()
	if len(items) != len(ops) {
		t.Fatalf("persisted item count mismatch: got %d persisted items, %d response operations", len(items), len(ops))
	}

	for i := range items {
		persistedTarget := ""
		if items[i].TargetPath != nil {
			persistedTarget = *items[i].TargetPath
		}

		if items[i].SourcePath != ops[i].GetSourcePath() || persistedTarget != ops[i].GetTargetPath() {
			t.Fatalf(
				"sequence mismatch at index %d: persisted=(%q -> %q), response=(%q -> %q)",
				i,
				items[i].SourcePath,
				persistedTarget,
				ops[i].GetSourcePath(),
				ops[i].GetTargetPath(),
			)
		}
	}
}

func TestPlanOperations_Slim_FolderScope_EnrichesBitrateBeforeAnalyze(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-bitrate-enrich-*")
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

	mp3Path := filepath.Join(tmpDir, "track.mp3")
	flacPath := filepath.Join(tmpDir, "track.flac")
	writeMinimalMP3Frame(t, mp3Path)
	if err := os.WriteFile(flacPath, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create flac file: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, NULL)
	`, filepath.ToSlash(mp3Path), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert mp3 entry: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(flacPath), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert flac entry: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		FolderPath: tmpDir,
		PlanType:   "slim",
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	if resp.GetActionableCount() == 0 {
		t.Fatalf("expected actionable operations after bitrate enrichment, got 0 (total=%d, reason=%s)", resp.GetTotalCount(), resp.GetSummaryReason())
	}

	var bitrate int64
	if err := repo.DB().QueryRow("SELECT COALESCE(bitrate, 0) FROM entries WHERE path = ?", filepath.ToSlash(mp3Path)).Scan(&bitrate); err != nil {
		t.Fatalf("failed to read enriched bitrate: %v", err)
	}
	if bitrate <= 0 {
		t.Fatalf("expected persisted mp3 bitrate > 0 after planning, got %d", bitrate)
	}
}

func TestPlanOperations_Slim_UsesEncoderExtensionForConvertTargets(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-encoder-ext-*")
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

	flacPath := filepath.Join(tmpDir, "track.flac")
	mp3Path := filepath.Join(tmpDir, "track.mp3")
	if err := os.WriteFile(flacPath, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create flac file: %v", err)
	}
	if err := os.WriteFile(mp3Path, []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create mp3 file: %v", err)
	}

	// FLAC + low bitrate MP3 => slim mode should generate convert operation
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(flacPath), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert flac entry: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, 192000)
	`, filepath.ToSlash(mp3Path), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert mp3 entry: %v", err)
	}

	// Configure lame encoder: convert target suffix must be .mp3
	configJSON := `{
		"tools": {
			"encoder": "lame",
			"lame_path": "C:/tools/lame.exe"
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		FolderPath: tmpDir,
		PlanType:   "slim",
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	foundConvert := false
	for _, it := range items {
		if it.OpType != "convert_and_delete" || it.TargetPath == nil {
			continue
		}
		foundConvert = true
		if filepath.Ext(*it.TargetPath) != ".mp3" {
			t.Fatalf("expected convert target extension .mp3 for lame encoder, got %s", filepath.Ext(*it.TargetPath))
		}
	}

	if !foundConvert {
		t.Fatal("expected at least one convert_and_delete item")
	}
}

func TestPlanOperations_Slim_SourceFilesOnly_DoesNotFallbackToGlobalAnalyze(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-source-files-only-*")
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

	inScopeDir := filepath.Join(tmpDir, "in_scope")
	outScopeDir := filepath.Join(tmpDir, "out_scope")
	if err := os.MkdirAll(inScopeDir, 0755); err != nil {
		t.Fatalf("failed to create in-scope dir: %v", err)
	}
	if err := os.MkdirAll(outScopeDir, 0755); err != nil {
		t.Fatalf("failed to create out-of-scope dir: %v", err)
	}

	inScopeMP3 := filepath.Join(inScopeDir, "song.mp3")
	outScopeFLAC := filepath.Join(outScopeDir, "song.flac")

	for _, p := range []string{inScopeMP3, outScopeFLAC} {
		if err := os.WriteFile(p, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create test file %q: %v", p, err)
		}
	}

	for _, tc := range []struct {
		path   string
		format string
		size   int
	}{
		{path: inScopeMP3, format: "audio/mpeg", size: 1000},
		{path: outScopeFLAC, format: "audio/flac", size: 2000},
	} {
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, ?, ?, 1, ?, 192000)
		`, filepath.ToSlash(tc.path), filepath.ToSlash(tmpDir), tc.size, tc.format, 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry %q: %v", tc.path, err)
		}
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:    "slim",
		SourceFiles: []string{inScopeMP3},
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	for _, pe := range resp.GetPlanErrors() {
		if pe.GetCode() == "SLIM_MODE1_LOSSLESS_ONLY" {
			t.Fatalf("unexpected out-of-scope mode1 error from global fallback: %+v", pe)
		}
	}

	outScopePrefix := filepath.ToSlash(outScopeDir) + "/"
	for _, op := range resp.GetOperations() {
		if strings.HasPrefix(op.GetSourcePath(), outScopePrefix) {
			t.Fatalf("unexpected out-of-scope source operation: %q", op.GetSourcePath())
		}
		if strings.HasPrefix(op.GetTargetPath(), outScopePrefix) {
			t.Fatalf("unexpected out-of-scope target operation: %q", op.GetTargetPath())
		}
	}
}

func TestPlanOperations_SlimMode2_PruneMatchedExcluded_ExcludesMatchedEntriesBeforeAnalyze(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-mode2-prune-by-pattern-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	wavPath := filepath.Join(tmpDir, "song.wav")
	mp3Path := filepath.Join(tmpDir, "song.mp3")
	if err := os.WriteFile(wavPath, []byte("dummy wav"), 0644); err != nil {
		t.Fatalf("failed to create wav file: %v", err)
	}
	if err := os.WriteFile(mp3Path, []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create mp3 file: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/wav', 1, ?)
	`, filepath.ToSlash(wavPath), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert wav entry: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, 320000)
	`, filepath.ToSlash(mp3Path), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert mp3 entry: %v", err)
	}

	configJSON := `{
		"prune": {
			"regex_pattern": "\\.wav$"
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:             "slim",
		TargetFormat:         "slim:mode2",
		FolderPath:           tmpDir,
		PruneMatchedExcluded: true,
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	for _, op := range resp.GetOperations() {
		if op.GetSourcePath() == filepath.ToSlash(wavPath) {
			t.Fatalf("expected wav entry to be excluded before slim mode2 analysis, found operation on %q", op.GetSourcePath())
		}
	}
}

func TestPlanOperations_SlimMode1_PruneMatchedExcluded_ExcludesMatchedEntriesBeforeAnalyze(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-mode1-prune-matched-excluded-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	wavPath := filepath.Join(tmpDir, "song.wav")
	mp3Path := filepath.Join(tmpDir, "song.mp3")
	if err := os.WriteFile(wavPath, []byte("dummy wav"), 0644); err != nil {
		t.Fatalf("failed to create wav file: %v", err)
	}
	if err := os.WriteFile(mp3Path, []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create mp3 file: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/wav', 1, ?)
	`, filepath.ToSlash(wavPath), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert wav entry: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, 320000)
	`, filepath.ToSlash(mp3Path), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert mp3 entry: %v", err)
	}

	configJSON := `{
		"prune": {
			"regex_pattern": "\\.wav$"
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:             "slim",
		TargetFormat:         "slim:mode1",
		FolderPath:           tmpDir,
		PruneMatchedExcluded: true,
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	for _, op := range resp.GetOperations() {
		if op.GetSourcePath() == filepath.ToSlash(wavPath) {
			t.Fatalf("expected wav entry to be excluded before slim mode1 analysis, found operation on %q", op.GetSourcePath())
		}
	}
}

func TestPlanOperations_Slim_SourceFilesOnly_EnrichesBitrateBeforeAnalyze_DefaultMode2(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-source-files-bitrate-enrich-*")
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

	inScopeDir := filepath.Join(tmpDir, "in_scope")
	outScopeDir := filepath.Join(tmpDir, "out_scope")
	if err := os.MkdirAll(inScopeDir, 0755); err != nil {
		t.Fatalf("failed to create in-scope dir: %v", err)
	}
	if err := os.MkdirAll(outScopeDir, 0755); err != nil {
		t.Fatalf("failed to create out-of-scope dir: %v", err)
	}

	inScopeMP3 := filepath.Join(inScopeDir, "track.mp3")
	inScopeFLAC := filepath.Join(inScopeDir, "track.flac")
	outScopeMP3 := filepath.Join(outScopeDir, "other.mp3")

	writeMinimalMP3Frame(t, inScopeMP3)
	if err := os.WriteFile(inScopeFLAC, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create in-scope flac: %v", err)
	}
	writeMinimalMP3Frame(t, outScopeMP3)

	for _, tc := range []struct {
		path    string
		format  string
		size    int
		bitrate interface{}
	}{
		{path: inScopeMP3, format: "audio/mpeg", size: 1000, bitrate: nil},
		{path: inScopeFLAC, format: "audio/flac", size: 2000, bitrate: nil},
		{path: outScopeMP3, format: "audio/mpeg", size: 1000, bitrate: nil},
	} {
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, ?, ?, 1, ?, ?)
		`, filepath.ToSlash(tc.path), filepath.ToSlash(tmpDir), tc.size, tc.format, 1234567890, tc.bitrate)
		if err != nil {
			t.Fatalf("failed to insert entry %q: %v", tc.path, err)
		}
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:    "slim",
		SourceFiles: []string{inScopeMP3, inScopeFLAC},
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	foundConvert := false
	for _, op := range resp.GetOperations() {
		if op.GetOperationType() == "convert" || op.GetOperationType() == "convert_and_delete" {
			foundConvert = true
			break
		}
	}
	if !foundConvert {
		t.Fatalf("expected convert operation in default slim source_files mode (mode2), got ops: %+v", resp.GetOperations())
	}

	var inScopeBitrate int64
	if err := repo.DB().QueryRow("SELECT COALESCE(bitrate, 0) FROM entries WHERE path = ?", filepath.ToSlash(inScopeMP3)).Scan(&inScopeBitrate); err != nil {
		t.Fatalf("failed to read in-scope bitrate: %v", err)
	}
	if inScopeBitrate <= 0 {
		t.Fatalf("expected in-scope mp3 bitrate > 0 after source_files planning, got %d", inScopeBitrate)
	}

	var outScopeBitrate int64
	if err := repo.DB().QueryRow("SELECT COALESCE(bitrate, 0) FROM entries WHERE path = ?", filepath.ToSlash(outScopeMP3)).Scan(&outScopeBitrate); err != nil {
		t.Fatalf("failed to read out-of-scope bitrate: %v", err)
	}
	if outScopeBitrate != 0 {
		t.Fatalf("expected out-of-scope bitrate to remain 0, got %d", outScopeBitrate)
	}
}

func TestPlanOperations_Slim_NoScope_ReturnsGlobalNoScope(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-no-scope-*")
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

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{PlanType: "slim"})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	if resp.GetSummaryReason() != "GLOBAL_SHORT_CIRCUIT" {
		t.Fatalf("expected summary_reason GLOBAL_SHORT_CIRCUIT, got %q", resp.GetSummaryReason())
	}
	if len(resp.GetPlanErrors()) == 0 {
		t.Fatalf("expected plan_errors, got none")
	}
	if got := resp.GetPlanErrors()[0].GetCode(); got != "GLOBAL_NO_SCOPE" {
		t.Fatalf("expected first plan error code GLOBAL_NO_SCOPE, got %q", got)
	}
}
