package plan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// =============================================================================
// Helpers for usecase plan integration tests
// =============================================================================

func newTestRepo(t *testing.T, dir string) *sqlite.Repository {
	t.Helper()
	repo, err := sqlite.NewRepository(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

func insertEntry(t *testing.T, repo *sqlite.Repository, path, rootPath, format string, size int64, bitrate interface{}) {
	t.Helper()
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, ?, ?, 1, ?, ?)
	`, filepath.ToSlash(path), filepath.ToSlash(rootPath), size, format, 1234567890, bitrate)
	if err != nil {
		t.Fatalf("insert entry %q: %v", path, err)
	}
}

func writeDummyFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("dummy"), 0644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func writeMinimalMP3(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir for %q: %v", path, err)
	}
	data := append([]byte{0xFF, 0xFB, 0x90, 0x64}, make([]byte, 1024)...)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write minimal mp3 %s: %v", path, err)
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
}

// =============================================================================
// Slim folder scope tests (ported from grpc/plan_rpc_basic_slim_test.go)
// =============================================================================

func TestPlan_Slim_PersistsPlanType(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	testFile1 := filepath.Join(tmpDir, "test1.mp3")
	testFile2 := filepath.Join(tmpDir, "test2.flac")
	writeDummyFile(t, testFile1)
	writeDummyFile(t, testFile2)

	insertEntry(t, repo, testFile1, tmpDir, "audio/mpeg", 1000, nil)
	insertEntry(t, repo, testFile2, tmpDir, "audio/flac", 2000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{FolderPath: tmpDir, PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if resp.PlanID == "" {
		t.Error("plan_id should not be empty")
	}

	plan, err := repo.GetPlan(resp.PlanID)
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}
	if plan.PlanType != "slim" {
		t.Errorf("expected plan_type 'slim', got %q", plan.PlanType)
	}
}

func TestPlan_Slim_FolderScope_EnrichesBitrateBeforeAnalyze(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	mp3Path := filepath.Join(tmpDir, "track.mp3")
	flacPath := filepath.Join(tmpDir, "track.flac")
	writeMinimalMP3(t, mp3Path)
	writeDummyFile(t, flacPath)

	insertEntry(t, repo, mp3Path, tmpDir, "audio/mpeg", 1000, nil)
	insertEntry(t, repo, flacPath, tmpDir, "audio/flac", 2000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{FolderPath: tmpDir, PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if resp.Summary.ActionableCount == 0 {
		t.Fatalf("expected actionable operations after bitrate enrichment, got total=%d", resp.Summary.TotalCount)
	}

	var bitrate int64
	if err := repo.DB().QueryRow("SELECT COALESCE(bitrate, 0) FROM entries WHERE path = ?", filepath.ToSlash(mp3Path)).Scan(&bitrate); err != nil {
		t.Fatalf("failed to read enriched bitrate: %v", err)
	}
	if bitrate <= 0 {
		t.Fatalf("expected persisted mp3 bitrate > 0 after planning, got %d", bitrate)
	}
}

func TestPlan_Slim_UsesEncoderExtensionForConvertTargets(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	flacPath := filepath.Join(tmpDir, "track.flac")
	mp3Path := filepath.Join(tmpDir, "track.mp3")
	writeDummyFile(t, flacPath)
	writeDummyFile(t, mp3Path)

	insertEntry(t, repo, flacPath, tmpDir, "audio/flac", 2000, nil)
	insertEntry(t, repo, mp3Path, tmpDir, "audio/mpeg", 1000, int64(192000))

	writeConfig(t, tmpDir, `{"tools": {"encoder": "lame", "lame_path": "C:/tools/lame.exe"}}`)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{FolderPath: tmpDir, PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanID)
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

func TestPlan_Slim_SourceFilesOnly_DoesNotFallbackToGlobalAnalyze(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	inScopeDir := filepath.Join(tmpDir, "in_scope")
	outScopeDir := filepath.Join(tmpDir, "out_scope")
	inScopeMP3 := filepath.Join(inScopeDir, "song.mp3")
	outScopeFLAC := filepath.Join(outScopeDir, "song.flac")

	writeDummyFile(t, inScopeMP3)
	writeDummyFile(t, outScopeFLAC)

	insertEntry(t, repo, inScopeMP3, tmpDir, "audio/mpeg", 1000, int64(192000))
	insertEntry(t, repo, outScopeFLAC, tmpDir, "audio/flac", 2000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{PlanType: "slim", SourceFiles: []string{inScopeMP3}})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify no out-of-scope operations
	outScopePrefix := filepath.ToSlash(outScopeDir) + "/"
	for _, op := range resp.Operations {
		if strings.HasPrefix(op.SourcePath, outScopePrefix) {
			t.Fatalf("unexpected out-of-scope source operation: %q", op.SourcePath)
		}
		if strings.HasPrefix(op.TargetPath, outScopePrefix) {
			t.Fatalf("unexpected out-of-scope target operation: %q", op.TargetPath)
		}
	}

	// Verify no error codes from global fallback
	for _, pe := range resp.Errors {
		if pe.Code == "SLIM_MODE1_LOSSLESS_ONLY" {
			t.Fatalf("unexpected out-of-scope mode1 error from global fallback: %+v", pe)
		}
	}
}

func TestPlan_Slim_NoScope_ReturnsGlobalNoScope(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if resp.Summary.SummaryReason != "GLOBAL_SHORT_CIRCUIT" {
		t.Fatalf("expected summary_reason GLOBAL_SHORT_CIRCUIT, got %q", resp.Summary.SummaryReason)
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected errors, got none")
	}
	if got := resp.Errors[0].Code; got != "GLOBAL_NO_SCOPE" {
		t.Fatalf("expected first error code GLOBAL_NO_SCOPE, got %q", got)
	}
}

// =============================================================================
// Slim prune-matched-excluded tests (ported from grpc/plan_rpc_basic_slim_test.go)
// =============================================================================

func TestPlan_SlimMode2_PruneMatchedExcluded_ExcludesMatchedEntries(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	wavPath := filepath.Join(tmpDir, "song.wav")
	mp3Path := filepath.Join(tmpDir, "song.mp3")
	writeDummyFile(t, wavPath)
	writeDummyFile(t, mp3Path)

	insertEntry(t, repo, wavPath, tmpDir, "audio/wav", 2000, nil)
	insertEntry(t, repo, mp3Path, tmpDir, "audio/mpeg", 1000, int64(320000))

	writeConfig(t, tmpDir, `{"prune": {"regex_pattern": "\\.wav$"}}`)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		PlanType:             "slim",
		TargetFormat:         "slim:mode2",
		FolderPath:           tmpDir,
		PruneMatchedExcluded: true,
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	wavSlash := filepath.ToSlash(wavPath)
	for _, op := range resp.Operations {
		if op.SourcePath == wavSlash {
			t.Fatalf("expected wav entry to be excluded before slim mode2 analysis, found operation on %q", op.SourcePath)
		}
	}
}

func TestPlan_SlimMode1_PruneMatchedExcluded_ExcludesMatchedEntries(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	wavPath := filepath.Join(tmpDir, "song.wav")
	mp3Path := filepath.Join(tmpDir, "song.mp3")
	writeDummyFile(t, wavPath)
	writeDummyFile(t, mp3Path)

	insertEntry(t, repo, wavPath, tmpDir, "audio/wav", 2000, nil)
	insertEntry(t, repo, mp3Path, tmpDir, "audio/mpeg", 1000, int64(320000))

	writeConfig(t, tmpDir, `{"prune": {"regex_pattern": "\\.wav$"}}`)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		PlanType:             "slim",
		TargetFormat:         "slim:mode1",
		FolderPath:           tmpDir,
		PruneMatchedExcluded: true,
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	wavSlash := filepath.ToSlash(wavPath)
	for _, op := range resp.Operations {
		if op.SourcePath == wavSlash {
			t.Fatalf("expected wav entry to be excluded before slim mode1 analysis, found operation on %q", op.SourcePath)
		}
	}
}

// =============================================================================
// Delete target persistence tests (ported from grpc/plan_rpc_delete_target_test.go)
// =============================================================================

func TestPlan_Slim_DeleteTargetPath_Persistence(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	musicFolder := filepath.Join(tmpDir, "Music")
	flacFile := filepath.Join(musicFolder, "song.flac")
	mp3File := filepath.Join(musicFolder, "song.mp3")
	writeDummyFile(t, flacFile)
	writeDummyFile(t, mp3File)

	insertEntry(t, repo, flacFile, tmpDir, "audio/flac", 2000, nil)
	insertEntry(t, repo, mp3File, tmpDir, "audio/mpeg", 1000, int64(192000))

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		FolderPath:   musicFolder,
		PlanType:     "slim",
		TargetFormat: "slim:mode2",
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanID)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	foundDelete := false
	for _, item := range items {
		if item.OpType == "delete" {
			foundDelete = true
			if item.TargetPath == nil || *item.TargetPath == "" {
				t.Errorf("delete item should have non-empty target_path (soft-delete destination), got nil or empty")
			} else {
				expectedPrefix := filepath.ToSlash(tmpDir) + "/Delete/"
				if !strings.HasPrefix(*item.TargetPath, expectedPrefix) {
					t.Errorf("expected target_path to start with %q, got %q", expectedPrefix, *item.TargetPath)
				}
			}
		}
	}
	if !foundDelete {
		t.Fatal("expected at least one delete item in plan")
	}
}

// =============================================================================
// Error attribution tests (ported from grpc/plan_rpc_errors_test.go)
// =============================================================================

func TestPlan_PartialSuccess_FolderErrorsAndSuccessfulFolders(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	validFolder2 := filepath.Join(tmpDir, "2_valid_music")
	invalidFolder := filepath.Join(tmpDir, "5_invalid_folder")
	validFolder10 := filepath.Join(tmpDir, "10_valid_music")

	writeDummyFile(t, filepath.Join(validFolder2, "song2.flac"))
	writeDummyFile(t, filepath.Join(validFolder10, "song10.flac"))

	insertEntry(t, repo, filepath.Join(validFolder2, "song2.flac"), tmpDir, "audio/flac", 2000, nil)
	insertEntry(t, repo, filepath.Join(validFolder10, "song10.flac"), tmpDir, "audio/flac", 2000, nil)
	insertEntry(t, repo, filepath.ToSlash(invalidFolder)+"/test\x00song.mp3", tmpDir, "audio/mpeg", 1000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		FolderPaths: []string{validFolder10, invalidFolder, validFolder2},
		PlanType:    "slim",
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(resp.Errors) == 0 {
		t.Fatal("expected errors to contain at least one error for invalid folder")
	}

	foundInvalidFolder := false
	for _, pe := range resp.Errors {
		if pe.FolderPath != "" {
			foundInvalidFolder = true
			if pe.Code == "" {
				t.Error("expected non-empty error code")
			}
			break
		}
	}
	if !foundInvalidFolder {
		t.Error("expected errors to contain an error with non-empty folder_path")
	}

	if len(resp.SuccessfulFolders) != 2 {
		t.Errorf("expected 2 successful folders, got %d", len(resp.SuccessfulFolders))
	}

	// Verify error events persisted to DB
	events, err := repo.ListErrorEventsByRoot(filepath.ToSlash(tmpDir))
	if err != nil {
		t.Fatalf("failed to list error events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Code == "PATH_NULL_BYTE" {
			found = true
			if e.Scope != "slim" {
				t.Errorf("expected scope='slim', got %q", e.Scope)
			}
			if e.Retryable {
				t.Error("expected retryable=false for plan error")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected PATH_NULL_BYTE error event persisted, got: %+v", events)
	}
}

func TestPlan_PartitionInvariant(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	folder1 := filepath.Join(tmpDir, "folder1")
	folder2 := filepath.Join(tmpDir, "folder2")
	folder3 := filepath.Join(tmpDir, "folder3")

	writeDummyFile(t, filepath.Join(folder1, "song.flac"))
	writeDummyFile(t, filepath.Join(folder3, "song.flac"))

	insertEntry(t, repo, filepath.Join(folder1, "song.flac"), tmpDir, "audio/flac", 2000, nil)
	insertEntry(t, repo, filepath.ToSlash(folder2)+"/bad\x00file.mp3", tmpDir, "audio/mpeg", 1000, nil)
	insertEntry(t, repo, filepath.Join(folder3, "song.flac"), tmpDir, "audio/flac", 2000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		FolderPaths: []string{folder1, folder2, folder3},
		PlanType:    "slim",
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	errorFolders := make(map[string]bool)
	for _, pe := range resp.Errors {
		if pe.FolderPath != "" {
			errorFolders[pe.FolderPath] = true
		}
	}

	successFolders := make(map[string]bool)
	for _, sf := range resp.SuccessfulFolders {
		successFolders[sf] = true
	}

	requestFolders := []string{
		filepath.ToSlash(filepath.Clean(folder1)),
		filepath.ToSlash(filepath.Clean(folder2)),
		filepath.ToSlash(filepath.Clean(folder3)),
	}

	for _, rf := range requestFolders {
		inError := errorFolders[rf]
		inSuccess := successFolders[rf]

		if inError && inSuccess {
			t.Errorf("folder %q is in both error and success sets", rf)
		}
		if !inError && !inSuccess {
			t.Errorf("folder %q is in neither error nor success sets (partition violation)", rf)
		}
	}
}

func TestPlan_GlobalShortCircuit(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{PlanType: "slim"})
	if err != nil {
		t.Fatalf("expected response with global error, got error: %v", err)
	}

	if len(resp.Errors) == 0 {
		t.Fatal("expected errors to contain at least one error")
	}

	foundGlobal := false
	for _, pe := range resp.Errors {
		if pe.FolderPath == "" {
			foundGlobal = true
			if pe.Code == "" {
				t.Error("expected non-empty error code for global error")
			}
			break
		}
	}
	if !foundGlobal {
		t.Error("expected errors to contain a global error (folder_path=='')")
	}

	if resp.Summary.SummaryReason != "GLOBAL_SHORT_CIRCUIT" {
		t.Errorf("expected summary_reason='GLOBAL_SHORT_CIRCUIT', got %q", resp.Summary.SummaryReason)
	}

	// Verify GLOBAL_NO_SCOPE error event persisted to DB
	events, err := repo.ListErrorEventsByRoot("")
	if err != nil {
		t.Fatalf("failed to list error events: %v", err)
	}
	foundPersisted := false
	for _, e := range events {
		if e.Code == "GLOBAL_NO_SCOPE" {
			foundPersisted = true
			if e.Scope != "slim" {
				t.Errorf("expected scope='slim', got %q", e.Scope)
			}
			if e.Retryable {
				t.Error("expected retryable=false")
			}
			break
		}
	}
	if !foundPersisted {
		t.Errorf("expected GLOBAL_NO_SCOPE error event persisted, got: %+v", events)
	}
}

func TestPlan_StableErrorCodes_PATH_ABS_FAILED(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	folder := filepath.Join(tmpDir, "folder")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	oldAbs := FilepathAbs
	FilepathAbs = func(_ string) (string, error) {
		return "", errors.New("forced abs failure")
	}
	defer func() { FilepathAbs = oldAbs }()

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{FolderPaths: []string{folder}, PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	found := false
	for _, pe := range resp.Errors {
		if pe.Code == "PATH_ABS_FAILED" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PATH_ABS_FAILED in errors, got: %+v", resp.Errors)
	}
}

func TestPlan_StableErrorCodes_PATH_NODE_TOO_LONG(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	longNameFolder := filepath.Join(tmpDir, "long_name_folder")
	if err := os.MkdirAll(longNameFolder, 0755); err != nil {
		t.Fatal(err)
	}

	longName := strings.Repeat("a", 300) + ".mp3"
	longPath := filepath.ToSlash(filepath.Join(longNameFolder, longName))
	insertEntry(t, repo, longPath, tmpDir, "audio/mpeg", 1000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{FolderPaths: []string{longNameFolder}, PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	found := false
	for _, pe := range resp.Errors {
		if pe.Code == "PATH_NODE_RUNE_TOO_LONG" || pe.Code == "PATH_NODE_BYTE_TOO_LONG" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error code PATH_NODE_RUNE_TOO_LONG or PATH_NODE_BYTE_TOO_LONG, got errors: %v", resp.Errors)
	}
}

func TestPlan_StableErrorCodes_SLIM_STEM_MATCH_GT2(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	stemFolder := filepath.Join(tmpDir, "stem_folder")
	if err := os.MkdirAll(stemFolder, 0755); err != nil {
		t.Fatal(err)
	}

	insertEntry(t, repo, filepath.Join(stemFolder, "song.flac"), tmpDir, "audio/flac", 2000, nil)
	insertEntry(t, repo, filepath.Join(stemFolder, "song.mp3"), tmpDir, "audio/mpeg", 1000, int64(192000))
	insertEntry(t, repo, filepath.Join(stemFolder, "song.wav"), tmpDir, "audio/wav", 3000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		FolderPaths:  []string{stemFolder},
		PlanType:     "slim",
		TargetFormat: "slim:mode2",
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	foundCode := false
	for _, pe := range resp.Errors {
		if pe.Code == "SLIM_STEM_MATCH_GT2" {
			foundCode = true
		}
	}
	if !foundCode {
		t.Fatalf("expected SLIM_STEM_MATCH_GT2 in errors, got: %+v", resp.Errors)
	}

	folderNorm := filepath.ToSlash(filepath.Clean(stemFolder))
	for _, sf := range resp.SuccessfulFolders {
		if sf == folderNorm {
			t.Fatalf("folder with SLIM_STEM_MATCH_GT2 must not be in successful_folders")
		}
	}
	if len(resp.Operations) != 0 {
		t.Fatalf("failed folder must not contribute operations, got %d ops", len(resp.Operations))
	}
}

func TestPlan_StableErrorCodes_SLIM_MODE1_LOSSLESS_ONLY(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	folder := filepath.Join(tmpDir, "lossless_only")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	insertEntry(t, repo, filepath.Join(folder, "song.wav"), tmpDir, "audio/wav", 1000, nil)
	insertEntry(t, repo, filepath.Join(folder, "alt.flac"), tmpDir, "audio/flac", 2000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		FolderPaths:  []string{folder},
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	foundCode := false
	for _, pe := range resp.Errors {
		if pe.Code == "SLIM_MODE1_LOSSLESS_ONLY" {
			foundCode = true
		}
	}
	if !foundCode {
		t.Fatalf("expected SLIM_MODE1_LOSSLESS_ONLY in errors, got: %+v", resp.Errors)
	}

	folderNorm := filepath.ToSlash(filepath.Clean(folder))
	for _, sf := range resp.SuccessfulFolders {
		if sf == folderNorm {
			t.Fatalf("folder with SLIM_MODE1_LOSSLESS_ONLY must not be in successful_folders")
		}
	}
	if len(resp.Operations) != 0 {
		t.Fatalf("failed folder must not contribute operations, got %d ops", len(resp.Operations))
	}
}

// =============================================================================
// Prune scope tests (ported from grpc/plan_rpc_prune_scope_test.go)
// =============================================================================

func TestPlan_Prune_RespectsFolderPathScope(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	folderA := filepath.Join(tmpDir, "A")
	folderB := filepath.Join(tmpDir, "B")
	os.MkdirAll(folderA, 0755)
	os.MkdirAll(folderB, 0755)

	aMP3 := filepath.Join(folderA, "keep_song.mp3")
	aFLAC := filepath.Join(folderA, "keep_song.flac")
	bMP3 := filepath.Join(folderB, "keep_song.mp3")
	bFLAC := filepath.Join(folderB, "keep_song.flac")

	for _, f := range []string{aMP3, aFLAC, bMP3, bFLAC} {
		writeDummyFile(t, f)
	}

	insertEntry(t, repo, aMP3, tmpDir, "audio/mpeg", 1000, nil)
	insertEntry(t, repo, aFLAC, tmpDir, "audio/flac", 1000, nil)
	insertEntry(t, repo, bMP3, tmpDir, "audio/mpeg", 1000, nil)
	insertEntry(t, repo, bFLAC, tmpDir, "audio/flac", 1000, nil)

	writeConfig(t, tmpDir, `{"prune": {"regex_pattern": "keep_"}}`)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		PlanType:     "prune",
		TargetFormat: "prune:both",
		FolderPath:   filepath.ToSlash(folderA),
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	items, err := repo.ListPlanItems(resp.PlanID)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 prune operations under folderA scope, got %d", len(items))
	}

	for _, item := range items {
		if !strings.HasPrefix(filepath.ToSlash(item.SourcePath), filepath.ToSlash(folderA)+"/") {
			t.Errorf("expected scoped prune path under %q, got %q", filepath.ToSlash(folderA), item.SourcePath)
		}
	}
}

func TestPlan_Prune_NoScope_RequireScope_ReturnsGlobalNoScope(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	// Insert an entry that would produce operations if global scan ran
	testFile := filepath.Join(tmpDir, "song.mp3")
	writeDummyFile(t, testFile)
	insertEntry(t, repo, testFile, tmpDir, "audio/mpeg", 1000, nil)

	// Default config has require_scope=true
	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		PlanType:     "prune",
		TargetFormat: "prune:both",
		// No scope fields
	})
	if err != nil {
		t.Fatalf("Plan returned unexpected error: %v", err)
	}

	if resp.Summary.SummaryReason != "GLOBAL_SHORT_CIRCUIT" {
		t.Fatalf("expected summary_reason GLOBAL_SHORT_CIRCUIT, got %q", resp.Summary.SummaryReason)
	}
	if len(resp.Errors) == 0 {
		t.Fatal("expected at least one error (GLOBAL_NO_SCOPE)")
	}
	if resp.Errors[0].Code != "GLOBAL_NO_SCOPE" {
		t.Errorf("expected GLOBAL_NO_SCOPE, got %q", resp.Errors[0].Code)
	}
}

// =============================================================================
// Summary field tests
// =============================================================================

func TestPlan_SummaryFields_ActionableAndTotal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	flacFile := filepath.Join(tmpDir, "song.flac")
	mp3File := filepath.Join(tmpDir, "song.mp3")
	writeDummyFile(t, flacFile)
	writeDummyFile(t, mp3File)

	insertEntry(t, repo, flacFile, tmpDir, "audio/flac", 2000, nil)
	insertEntry(t, repo, mp3File, tmpDir, "audio/mpeg", 1000, int64(192000))

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{FolderPath: tmpDir, PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if resp.Summary.TotalCount <= 0 {
		t.Errorf("expected positive total_count, got %d", resp.Summary.TotalCount)
	}
	if resp.Summary.ActionableCount <= 0 {
		t.Errorf("expected positive actionable_count, got %d", resp.Summary.ActionableCount)
	}
	if resp.Summary.SummaryReason != "ACTIONABLE" {
		t.Errorf("expected summary_reason ACTIONABLE, got %q", resp.Summary.SummaryReason)
	}
}

func TestPlan_SummaryFields_EmptyCase_NoMatch(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	// Folder with only lossless files, mode1 should produce no operations
	folder := filepath.Join(tmpDir, "folder")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	flacOnly := filepath.Join(folder, "unique.flac")
	writeDummyFile(t, flacOnly)
	insertEntry(t, repo, flacOnly, tmpDir, "audio/flac", 2000, nil)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{
		FolderPaths:  []string{folder},
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if resp.Summary.OperationCount != 0 {
		t.Errorf("expected 0 operations (no match), got %d", resp.Summary.OperationCount)
	}
	if resp.Summary.SummaryReason == "ACTIONABLE" {
		t.Error("expected non-ACTIONABLE summary reason for empty result")
	}
}

// =============================================================================
// Prune config guard tests
// =============================================================================

func TestPlan_Prune_NoConfigFile_ReturnsError(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	folder := filepath.Join(tmpDir, "A")
	os.MkdirAll(folder, 0755)
	aMP3 := filepath.Join(folder, "song.mp3")
	writeDummyFile(t, aMP3)
	insertEntry(t, repo, aMP3, tmpDir, "audio/mpeg", 1000, nil)

	// No config.json — prune should fail when it tries to read regex_pattern
	svc := NewService(repo, tmpDir)
	_, err := svc.Plan(ctx, Request{
		PlanType:     "prune",
		TargetFormat: "prune:mp3aac",
		FolderPath:   filepath.ToSlash(folder),
	})
	if err == nil {
		t.Fatal("expected error for missing prune config, got nil")
	}
	t.Logf("correctly returned error for missing config: %v", err)
}

// =============================================================================
// Legacy global path test (require_scope=false)
// =============================================================================

func TestPlan_Slim_NoScope_RequireScopeDisabled_AllowsLegacyGlobalPath(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repo := newTestRepo(t, tmpDir)

	// Seed two audio entries: one lossless (FLAC), one lossy (MP3) forming a stem pair.
	// The global slim path should produce concrete operations when require_scope=false.
	mp3Path := filepath.Join(tmpDir, "song.mp3")
	flacPath := filepath.Join(tmpDir, "song.flac")
	writeDummyFile(t, mp3Path)
	writeDummyFile(t, flacPath)

	insertEntry(t, repo, mp3Path, tmpDir, "audio/mpeg", 1000, int64(192000))
	insertEntry(t, repo, flacPath, tmpDir, "audio/flac", 2000, nil)

	writeConfig(t, tmpDir, `{"plan": {"slim": {"require_scope": false}}}`)

	svc := NewService(repo, tmpDir)
	resp, err := svc.Plan(ctx, Request{PlanType: "slim"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// 1. GLOBAL_NO_SCOPE must NOT be present — require_scope=false allows legacy global path.
	for _, pe := range resp.Errors {
		if pe.Code == "GLOBAL_NO_SCOPE" {
			t.Fatalf("expected no GLOBAL_NO_SCOPE when require_scope=false, got response: %+v", resp)
		}
	}

	// 2. Summary must report ACTIONABLE (not GLOBAL_SHORT_CIRCUIT).
	if resp.Summary.SummaryReason != "ACTIONABLE" {
		t.Errorf("expected summary_reason ACTIONABLE (legacy global path), got %q", resp.Summary.SummaryReason)
	}

	// 3. Must produce operations from the global scan.
	if resp.Summary.OperationCount == 0 {
		t.Fatal("expected at least one operation from legacy global slim scan when DB has matching entries")
	}

	// 4. Root path must be determined from the scanned entries.
	if resp.RootPath == "" {
		t.Error("expected non-empty root_path from legacy global scan")
	}

	// 5. No folder errors for valid entries.
	if len(resp.Errors) != 0 {
		t.Errorf("expected 0 errors for valid entries in legacy global path, got %d: %+v", len(resp.Errors), resp.Errors)
	}
}
