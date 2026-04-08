package grpc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ============================================================================
// Task 3 Tests: PlanOperations Partial Success and Error Attribution
// ============================================================================

// TestPlanOperations_PlanErrors_PartialSuccess tests that when one folder fails
// and another succeeds, plan_errors contains the failure and successful_folders
// contains the success.
func TestPlanOperations_PlanErrors_PartialSuccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-planerrors-partial-*")
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

	// Create three subfolders: two valid, one invalid
	validFolder2 := filepath.Join(tmpDir, "2_valid_music")
	invalidFolder := filepath.Join(tmpDir, "5_invalid_folder")
	validFolder10 := filepath.Join(tmpDir, "10_valid_music")
	for _, folder := range []string{validFolder2, invalidFolder, validFolder10} {
		if err := os.MkdirAll(folder, 0755); err != nil {
			t.Fatalf("failed to create folder %q: %v", folder, err)
		}
	}

	// Create valid audio files in valid folders
	validFile2 := filepath.Join(validFolder2, "song2.flac")
	if err := os.WriteFile(validFile2, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create valid file: %v", err)
	}
	validFile10 := filepath.Join(validFolder10, "song10.flac")
	if err := os.WriteFile(validFile10, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create valid file: %v", err)
	}

	// Insert valid entries
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(validFile2), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert valid entry: %v", err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(validFile10), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert valid entry: %v", err)
	}

	// Create a file entry with a path that will fail validation (null byte)
	invalidPath := filepath.ToSlash(invalidFolder) + "/test\u0000song.mp3"
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, invalidPath, filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert invalid entry: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Plan with both folder paths
	req := &pb.PlanOperationsRequest{
		FolderPaths: []string{validFolder10, invalidFolder, validFolder2},
		PlanType:    "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Assert plan_errors contains the failure
	if len(resp.GetPlanErrors()) == 0 {
		t.Fatal("expected plan_errors to contain at least one error for invalid folder")
	}

	foundInvalidFolder := false
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetFolderPath() != "" {
			foundInvalidFolder = true
			// Assert structured error fields
			if pe.GetStage() != "plan" {
				t.Errorf("expected stage='plan', got %q", pe.GetStage())
			}
			if pe.GetCode() == "" {
				t.Error("expected non-empty error code")
			}
			if pe.GetRootPath() == "" {
				t.Error("expected non-empty root_path")
			}
			if pe.GetTimestamp() == "" {
				t.Error("expected non-empty timestamp")
			}
			if pe.GetEventId() == "" {
				t.Error("expected non-empty event_id")
			}
			break
		}
	}
	if !foundInvalidFolder {
		t.Error("expected plan_errors to contain an error with non-empty folder_path")
	}

	// Assert successful_folders remain deterministically sorted among successes.
	wantSuccess := []string{
		filepath.ToSlash(filepath.Clean(validFolder2)),
		filepath.ToSlash(filepath.Clean(validFolder10)),
	}
	if !reflect.DeepEqual(resp.GetSuccessfulFolders(), wantSuccess) {
		t.Errorf("unexpected successful_folders order: got=%v want=%v", resp.GetSuccessfulFolders(), wantSuccess)
	}

	// Assert failure does not perturb flattened successful operation order.
	gotOpFolders := make([]string, 0, len(resp.GetOperations()))
	for _, op := range resp.GetOperations() {
		source := filepath.FromSlash(op.GetSourcePath())
		gotOpFolders = append(gotOpFolders, filepath.Base(filepath.Dir(source)))
	}
	wantOpFolders := []string{"2_valid_music", "10_valid_music"}
	if !reflect.DeepEqual(gotOpFolders, wantOpFolders) {
		t.Errorf("unexpected operation folder order with partial failure: got=%v want=%v", gotOpFolders, wantOpFolders)
	}
}

// TestPlanOperations_PartitionInvariant tests that each request folder is
// classified either as error or success, except for global short-circuit failures.
func TestPlanOperations_PartitionInvariant(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-partition-invariant-*")
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

	// Create three folders: valid, invalid, another valid
	folder1 := filepath.Join(tmpDir, "folder1")
	folder2 := filepath.Join(tmpDir, "folder2")
	folder3 := filepath.Join(tmpDir, "folder3")
	for _, f := range []string{folder1, folder2, folder3} {
		if err := os.MkdirAll(f, 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}
	}

	// Valid file in folder1
	file1 := filepath.Join(folder1, "song.flac")
	if err := os.WriteFile(file1, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(file1), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatal(err)
	}

	// Invalid entry in folder2 (null byte in path)
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(folder2)+"/bad\u0000file.mp3", filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatal(err)
	}

	// Valid file in folder3
	file3 := filepath.Join(folder3, "song.flac")
	if err := os.WriteFile(file3, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(file3), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatal(err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPaths: []string{folder1, folder2, folder3},
		PlanType:    "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Build sets of folder paths
	errorFolders := make(map[string]bool)
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetFolderPath() != "" {
			errorFolders[pe.GetFolderPath()] = true
		}
	}

	successFolders := make(map[string]bool)
	for _, sf := range resp.GetSuccessfulFolders() {
		successFolders[sf] = true
	}

	// Verify partition: each requested folder should be in exactly one set
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

// TestPlanOperations_GlobalShortCircuit tests that request-level global
// short-circuit failures have folder_path == "".
func TestPlanOperations_GlobalShortCircuit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-global-shortcircuit-*")
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

	// Request with no scope at all - should be a global error
	req := &pb.PlanOperationsRequest{
		PlanType: "slim",
	}
	resp, err := server.PlanOperations(nil, req)

	// Should return a response with global error (folder_path==""), not RPC error
	if err != nil {
		t.Fatalf("expected response with global error, got RPC error: %v", err)
	}

	// Verify plan_errors contains a global error (folder_path=="")
	if len(resp.GetPlanErrors()) == 0 {
		t.Fatal("expected plan_errors to contain at least one error")
	}

	foundGlobal := false
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetFolderPath() == "" {
			foundGlobal = true
			// Verify error has required fields
			if pe.GetCode() == "" {
				t.Error("expected non-empty error code for global error")
			}
			if pe.GetStage() != "plan" {
				t.Errorf("expected stage='plan', got %q", pe.GetStage())
			}
			if pe.GetPlanId() == "" {
				t.Error("expected non-empty plan_id for global error")
			}
			break
		}
	}

	if !foundGlobal {
		t.Error("expected plan_errors to contain a global error (folder_path=='')")
	}

	// Verify summary_reason indicates global short-circuit
	if resp.GetSummaryReason() != "GLOBAL_SHORT_CIRCUIT" {
		t.Errorf("expected summary_reason='GLOBAL_SHORT_CIRCUIT', got %q", resp.GetSummaryReason())
	}
}

// TestPlanOperations_StableErrorCodes_PATH_ABS_FAILED tests the
// PATH_ABS_FAILED error code.
func TestPlanOperations_StableErrorCodes_PATH_ABS_FAILED(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-path-abs-failed-*")
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

	folder := filepath.Join(tmpDir, "folder")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	oldAbs := filepathAbs
	filepathAbs = func(_ string) (string, error) {
		return "", errors.New("forced abs failure")
	}
	defer func() { filepathAbs = oldAbs }()

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		FolderPaths: []string{folder},
		PlanType:    "slim",
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	found := false
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetCode() == "PATH_ABS_FAILED" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PATH_ABS_FAILED in plan_errors, got: %+v", resp.GetPlanErrors())
	}
}

// TestPlanOperations_StableErrorCodes_PATH_NODE_TOO_LONG tests the
// PATH_NODE_RUNE_TOO_LONG and PATH_NODE_BYTE_TOO_LONG error codes.
func TestPlanOperations_StableErrorCodes_PATH_NODE_TOO_LONG(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-path-node-toolong-*")
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

	// Create folder
	longNameFolder := filepath.Join(tmpDir, "long_name_folder")
	if err := os.MkdirAll(longNameFolder, 0755); err != nil {
		t.Fatal(err)
	}

	// Create entry with very long filename component (>255 chars)
	longName := strings.Repeat("a", 300) + ".mp3"
	longPath := filepath.ToSlash(filepath.Join(longNameFolder, longName))
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, longPath, filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatal(err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPaths: []string{longNameFolder},
		PlanType:    "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Should have plan_errors with path node too long code
	found := false
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetCode() == "PATH_NODE_RUNE_TOO_LONG" || pe.GetCode() == "PATH_NODE_BYTE_TOO_LONG" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error code PATH_NODE_RUNE_TOO_LONG or PATH_NODE_BYTE_TOO_LONG, got errors: %v", resp.GetPlanErrors())
	}
}

// TestPlanOperations_StableErrorCodes_SLIM_STEM_MATCH_GT2 tests the
// SLIM_STEM_MATCH_GT2 error code when more than 2 files match a stem.
func TestPlanOperations_StableErrorCodes_SLIM_STEM_MATCH_GT2(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-stem-gt2-*")
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

	// Create folder
	stemFolder := filepath.Join(tmpDir, "stem_folder")
	if err := os.MkdirAll(stemFolder, 0755); err != nil {
		t.Fatal(err)
	}

	// Create 3 files with same stem (FLAC + MP3 + another format)
	// This creates an ambiguous situation for slim mode
	stem := "song"
	formats := []string{".flac", ".mp3", ".wav"}
	for i, ext := range formats {
		path := filepath.ToSlash(filepath.Join(stemFolder, stem+ext))
		format := "audio/wav"
		bitrate := 0
		switch ext {
		case ".flac":
			format = "audio/flac"
		case ".mp3":
			format = "audio/mpeg"
			bitrate = 192000
		case ".wav":
			format = "audio/wav"
		}
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, ?, ?, 1, ?, ?)
		`, path, filepath.ToSlash(tmpDir), 1000+i*1000, format, 1234567890, bitrate)
		if err != nil {
			t.Fatal(err)
		}
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPaths:  []string{stemFolder},
		PlanType:     "slim",
		TargetFormat: "slim:mode2",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Must fail this folder with explicit stable error code.
	foundCode := false
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetCode() == "SLIM_STEM_MATCH_GT2" {
			foundCode = true
		}
	}
	if !foundCode {
		t.Fatalf("expected SLIM_STEM_MATCH_GT2 in plan_errors, got: %+v", resp.GetPlanErrors())
	}

	folderNorm := filepath.ToSlash(filepath.Clean(stemFolder))
	for _, sf := range resp.GetSuccessfulFolders() {
		if sf == folderNorm {
			t.Fatalf("folder with SLIM_STEM_MATCH_GT2 must not be in successful_folders: %v", resp.GetSuccessfulFolders())
		}
	}

	if len(resp.GetOperations()) != 0 {
		t.Fatalf("failed folder must not contribute operations, got %d ops", len(resp.GetOperations()))
	}
}

// TestPlanOperations_StableErrorCodes_SLIM_MODE1_LOSSLESS_ONLY tests mode1 lossless-only
// component error mapping into plan_errors.
func TestPlanOperations_StableErrorCodes_SLIM_MODE1_LOSSLESS_ONLY(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-mode1-lossless-only-*")
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

	folder := filepath.Join(tmpDir, "lossless_only")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	// Two lossless files only in same component.
	entries := []struct {
		path   string
		format string
		size   int
	}{
		{path: filepath.ToSlash(filepath.Join(folder, "song.wav")), format: "audio/wav", size: 1000},
		{path: filepath.ToSlash(filepath.Join(folder, "alt.flac")), format: "audio/flac", size: 2000},
	}

	for _, e := range entries {
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, ?, ?, 1, ?, ?)
		`, e.path, filepath.ToSlash(tmpDir), e.size, e.format, 1234567890, 0)
		if err != nil {
			t.Fatal(err)
		}
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPaths:  []string{folder},
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
	}

	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	foundCode := false
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetCode() == "SLIM_MODE1_LOSSLESS_ONLY" {
			foundCode = true
		}
	}
	if !foundCode {
		t.Fatalf("expected SLIM_MODE1_LOSSLESS_ONLY in plan_errors, got: %+v", resp.GetPlanErrors())
	}

	folderNorm := filepath.ToSlash(filepath.Clean(folder))
	for _, sf := range resp.GetSuccessfulFolders() {
		if sf == folderNorm {
			t.Fatalf("folder with SLIM_MODE1_LOSSLESS_ONLY must not be in successful_folders: %v", resp.GetSuccessfulFolders())
		}
	}

	if len(resp.GetOperations()) != 0 {
		t.Fatalf("failed folder must not contribute operations, got %d ops", len(resp.GetOperations()))
	}
}

// TestPlanOperations_PlanIDConflict_PropagatesAsAlreadyExists verifies that when
// a plan_id conflict occurs, the RPC returns AlreadyExists status code with
// PLAN_ID_CONFLICT message (not wrapped as Internal).
func TestPlanOperations_PlanIDConflict_PropagatesAsAlreadyExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-plan-id-conflict-*")
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

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// First call - should succeed
	req := &pb.PlanOperationsRequest{
		SourceFiles: []string{testFile},
		PlanType:    "single_delete",
	}
	resp1, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("first PlanOperations failed: %v", err)
	}
	if resp1.PlanId == "" {
		t.Fatal("expected plan_id in first response")
	}

	// Manually insert a plan with a specific ID to force conflict on second call
	// We'll create a plan directly in the DB, then try to create another with same ID
	planID := "conflict-test-plan-id"
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, snapshot_token, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, planID, "/music", "/music", "slim", "snap-1", "ready", "2024-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("failed to seed conflicting plan: %v", err)
	}

	// Verify conflicting plan exists
	_, err = repo.GetPlan(planID)
	if err != nil {
		t.Fatalf("failed to get seeded plan: %v", err)
	}

	// Now test the error propagation by using the persistPlan internal method
	// We'll simulate a scenario where persistPlan returns a PLAN_ID_CONFLICT
	// by calling it directly with the conflicting plan ID
	plan := &analyze.Plan{
		PlanID:        planID, // Use the conflicting ID
		SnapshotToken: "snap-2",
		Operations: []analyze.Operation{
			{Type: analyze.OpTypeDelete, SourcePath: filepath.ToSlash(testFile), Reason: "test"},
		},
	}

	err = server.persistPlan(planID, req, plan, "single_delete", true)
	if err == nil {
		t.Fatal("expected error for duplicate plan_id, got nil")
	}

	// Verify error is a gRPC status error with AlreadyExists code
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %T: %v", err, err)
	}

	if st.Code() != codes.AlreadyExists {
		t.Errorf("expected code AlreadyExists, got %v", st.Code())
	}

	if !strings.Contains(st.Message(), "PLAN_ID_CONFLICT") {
		t.Errorf("expected message to contain PLAN_ID_CONFLICT, got: %s", st.Message())
	}

	t.Logf("PLAN_ID_CONFLICT correctly propagated: code=%v, message=%s", st.Code(), st.Message())
}
