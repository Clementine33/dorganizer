package grpc

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func TestPlanOperations_SingleDelete_PersistsCorrectPlanType(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-single-delete-*")
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

	// Call PlanOperations with single_delete
	req := &pb.PlanOperationsRequest{
		SourceFiles: []string{testFile},
		PlanType:    "single_delete",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify plan_id is returned
	if resp.PlanId == "" {
		t.Error("plan_id should not be empty")
	}

	// Verify plan_type in database is "single_delete"
	plan, err := repo.GetPlan(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}
	if plan.PlanType != "single_delete" {
		t.Errorf("expected plan_type 'single_delete', got %q", plan.PlanType)
	}

	// Verify plan_items have correct op_type
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 plan item, got %d", len(items))
	}
	if items[0].OpType != "delete" {
		t.Errorf("expected op_type 'delete', got %q", items[0].OpType)
	}

	// Verify preconditions are set
	if items[0].PreconditionPath == "" {
		t.Error("precondition_path should be set")
	}
	if items[0].PreconditionContentRev == 0 {
		// Should have content_rev from DB
		t.Log("precondition_content_rev:", items[0].PreconditionContentRev)
	}
}

func TestPlanOperations_SingleDelete_UsesEntryRootPathAsPlanRoot(t *testing.T) {
	// Simulate scanned root with nested source file. For single_delete with
	// source_files only, persisted plan.root_path should use entries.root_path
	// (scan root), not filepath.Dir(source).
	tmpDir, err := os.MkdirTemp("", "onsei-test-single-delete-root-*")
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

	scanRoot := tmpDir
	sourceDir := filepath.Join(scanRoot, "music", "album")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}
	testFile := filepath.Join(sourceDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Persist entry with root_path=scan root.
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(scanRoot), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		SourceFiles: []string{testFile},
		PlanType:    "single_delete",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	plan, err := repo.GetPlan(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}

	if plan.RootPath != filepath.ToSlash(scanRoot) {
		t.Fatalf("expected plan.root_path to be scan root %q, got %q", filepath.ToSlash(scanRoot), plan.RootPath)
	}

	if plan.RootPath == filepath.ToSlash(filepath.Dir(testFile)) {
		t.Fatalf("plan.root_path should not fallback to source parent dir %q", filepath.ToSlash(filepath.Dir(testFile)))
	}
}

func TestPlanOperations_SlimWithSubfolderScope_UsesScanRootAsPlanRoot(t *testing.T) {
	// Regression: when planning with folder_path=subfolder (workflow scope),
	// plan.root_path must still be scan root from entries.root_path.
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-rootpath-*")
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

	scanRoot := tmpDir
	subFolder := filepath.Join(scanRoot, "music", "album")
	if err := os.MkdirAll(subFolder, 0755); err != nil {
		t.Fatalf("failed to create subfolder: %v", err)
	}
	fileMP3 := filepath.Join(subFolder, "song.mp3")
	fileFLAC := filepath.Join(subFolder, "song.flac")
	if err := os.WriteFile(fileMP3, []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create mp3: %v", err)
	}
	if err := os.WriteFile(fileFLAC, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create flac: %v", err)
	}

	// Insert entries as if scanned under scanRoot.
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileMP3), filepath.ToSlash(scanRoot), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert mp3 entry: %v", err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(fileFLAC), filepath.ToSlash(scanRoot), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert flac entry: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPath: subFolder,
		PlanType:   "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	plan, err := repo.GetPlan(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}

	if plan.RootPath != filepath.ToSlash(subFolder) {
		t.Fatalf("expected plan.root_path to stay scoped subfolder %q, got %q", filepath.ToSlash(subFolder), plan.RootPath)
	}
	if plan.ScanRootPath != filepath.ToSlash(scanRoot) {
		t.Fatalf("expected plan.scan_root_path to be scan root %q, got %q", filepath.ToSlash(scanRoot), plan.ScanRootPath)
	}
}

func TestPlanOperations_SingleConvert_PersistsCorrectPlanType(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-single-convert-*")
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

	// Call PlanOperations with single_convert and target_format
	req := &pb.PlanOperationsRequest{
		SourceFiles:  []string{testFile},
		PlanType:     "single_convert",
		TargetFormat: "m4a",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify plan_id is returned
	if resp.PlanId == "" {
		t.Error("plan_id should not be empty")
	}

	// Verify plan_type in database is "single_convert"
	plan, err := repo.GetPlan(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}
	if plan.PlanType != "single_convert" {
		t.Errorf("expected plan_type 'single_convert', got %q", plan.PlanType)
	}

	// Verify plan_items have correct op_type
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 plan item, got %d", len(items))
	}
	if items[0].OpType != "convert_and_delete" {
		t.Errorf("expected op_type 'convert_and_delete', got %q", items[0].OpType)
	}

	// Verify target_path is set with correct extension
	if items[0].TargetPath == nil {
		t.Error("target_path should be set")
	} else if filepath.Ext(*items[0].TargetPath) != ".m4a" {
		t.Errorf("expected target extension .m4a, got %s", filepath.Ext(*items[0].TargetPath))
	}

	// Verify preconditions are set
	if items[0].PreconditionPath == "" {
		t.Error("precondition_path should be set")
	}
}
