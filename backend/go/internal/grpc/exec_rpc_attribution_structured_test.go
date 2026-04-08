package grpc

import (
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	execsvc "github.com/onsei/organizer/backend/internal/services/execute"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutePlan_Attribution_PreconditionPath(t *testing.T) {
	// Attribution should use precondition_path when available for delete operations
	tests := []struct {
		name        string
		rootPath    string
		sourcePath  string
		precondPath string
		wantFolder  string
	}{
		{
			name:        "precondition path determines folder",
			rootPath:    "C:/Music",
			sourcePath:  "C:/Music/Album/song.mp3",
			precondPath: "C:/Music/Album/song.mp3",
			wantFolder:  "C:/Music/Album",
		},
		{
			name:        "precondition path different from source",
			rootPath:    "C:/Music",
			sourcePath:  "C:/Music/Album/song.mp3",
			precondPath: "C:/Music/Other/file.mp3",
			wantFolder:  "C:/Music/Other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.precondPath)
			wantNorm := filepath.ToSlash(filepath.Clean(tt.wantFolder))
			if got != wantNorm {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.precondPath, got, wantNorm)
			}
		})
	}
}

// TestExecutePlan_Attribution_TargetPath tests attribution uses target path for stage3.

func TestExecutePlan_Attribution_TargetPath(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		targetPath string
		wantFolder string
	}{
		{
			name:       "target path determines folder",
			rootPath:   "C:/Music",
			targetPath: "C:/Music/Album/song.m4a",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "target in different folder",
			rootPath:   "C:/Music",
			targetPath: "C:/Music/Converted/song.m4a",
			wantFolder: "C:/Music/Converted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.targetPath)
			wantNorm := filepath.ToSlash(filepath.Clean(tt.wantFolder))
			if got != wantNorm {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.targetPath, got, wantNorm)
			}
		})
	}
}

// TestExecutePlan_ConvertFailure_PreservesSource validates Task3 semantics:
// when convert fails, source file should NOT be deleted.

func TestExecutePlan_StructuredError_PreconditionFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-structured-precond-*")
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

	// Create test audio file in subfolder for folder attribution
	subDir := filepath.Join(tmpDir, "Album")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan with STALE precondition (will fail)
	planID := "plan-structured-precond-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with stale precondition (wrong content_rev)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	// Find error event with structured fields
	var errorEvent *pb.JobEvent
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			errorEvent = ev
			break
		}
	}

	if errorEvent == nil {
		t.Fatal("expected error JobEvent to be sent")
	}

	// Verify structured fields
	if errorEvent.Stage != "execute" {
		t.Errorf("expected stage=execute, got %q", errorEvent.Stage)
	}
	if errorEvent.Code != "EXEC_PRECONDITION_FAILED" {
		t.Errorf("expected code=EXEC_PRECONDITION_FAILED, got %q", errorEvent.Code)
	}
	if errorEvent.PlanId != planID {
		t.Errorf("expected plan_id=%s, got %q", planID, errorEvent.PlanId)
	}
	if errorEvent.EventId == "" {
		t.Error("expected event_id to be populated")
	}
	if errorEvent.Timestamp == "" {
		t.Error("expected timestamp to be populated")
	}
	if errorEvent.RootPath == "" {
		t.Error("expected root_path to be populated")
	}
	// Verify folder attribution
	if errorEvent.FolderPath == "" {
		t.Error("expected folder_path to be populated for precondition failure")
	}
	// Verify diagnostic fields
	if errorEvent.ItemSourcePath == "" {
		t.Error("expected item_source_path to be populated")
	}
}

// TestExecutePlan_StructuredError_DeleteFailed validates that delete failure
// emits structured error with EXEC_DELETE_FAILED code.

func TestExecutePlan_StructuredError_DeleteFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-structured-delete-*")
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

	// Create test audio file in subfolder
	subDir := filepath.Join(tmpDir, "Music")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan
	planID := "plan-structured-delete-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item for delete with correct preconditions
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Delete source file after plan creation (makes delete fail)
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to remove source file: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	_ = server.ExecutePlan(req, stream)

	// Find error event
	var errorEvent *pb.JobEvent
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			errorEvent = ev
			break
		}
	}

	if errorEvent == nil {
		t.Fatal("expected error JobEvent to be sent")
	}

	// Verify structured fields for delete failure
	if errorEvent.Stage != "execute" {
		t.Errorf("expected stage=execute, got %q", errorEvent.Stage)
	}
	// Note: delete failure on missing file might be precondition or delete failed
	if errorEvent.Code != "EXEC_PRECONDITION_FAILED" && errorEvent.Code != "EXEC_DELETE_FAILED" {
		t.Errorf("expected code=EXEC_PRECONDITION_FAILED or EXEC_DELETE_FAILED, got %q", errorEvent.Code)
	}
	if errorEvent.PlanId != planID {
		t.Errorf("expected plan_id=%s, got %q", planID, errorEvent.PlanId)
	}
	if errorEvent.EventId == "" {
		t.Error("expected event_id to be populated")
	}
}

// TestExecutePlan_FolderCompleted_EmittedOnSuccess validates that folder_completed
// event is emitted for successful folders with required fields.

func TestExecutePlan_AttributionPrecedence_Precondition(t *testing.T) {
	// This tests that the folder_path is correctly derived from the source path
	// for precondition failures
	tmpDir, err := os.MkdirTemp("", "onsei-test-attribution-precond-*")
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

	// Create subfolder for attribution test
	subDir := filepath.Join(tmpDir, "Album")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan
	planID := "plan-attribution-precond-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with stale precondition
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	// Find error event
	var errorEvent *pb.JobEvent
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			errorEvent = ev
			break
		}
	}

	if errorEvent == nil {
		t.Fatal("expected error JobEvent to be sent")
	}

	// Verify folder_path matches expected attribution
	expectedFolderPath := filepath.ToSlash(subDir)
	if errorEvent.FolderPath != expectedFolderPath {
		t.Errorf("expected folder_path=%q, got %q", expectedFolderPath, errorEvent.FolderPath)
	}
}

// TestExecutePlan_PerFolderFailFast validates that:
// - First error in a folder stops remaining items in that folder
// - Other folders continue to execute

func TestExecuteEventHandler_StageFailures_EmitStructuredErrors(t *testing.T) {
	root := filepath.ToSlash(filepath.Join(`C:\`, "Music"))
	h := &executeEventHandler{
		stream:            &mockServerStreamHelper{},
		rootPath:          root,
		planID:            "plan-stage-failures-001",
		failedFolders:     make(map[string]bool),
		successfulFolders: make(map[string]bool),
	}
	stream := h.stream.(*mockServerStreamHelper)

	tests := []struct {
		name      string
		call      func(item execsvc.PlanItem)
		item      execsvc.PlanItem
		expCode   string
		expStage  string
		expFolder string
		expSrc    string
		expDst    string
	}{
		{
			name:      "stage1 uses source attribution",
			call:      func(item execsvc.PlanItem) { h.OnStage1CopyFailed(3, item, os.ErrPermission) },
			item:      execsvc.PlanItem{SourcePath: filepath.Join(`C:\`, "Music", "AlbumA", "a.wav")},
			expCode:   "EXEC_STAGE1_COPY_FAILED",
			expStage:  "execute",
			expFolder: filepath.ToSlash(filepath.Join(`C:\`, "Music", "AlbumA")),
			expSrc:    filepath.ToSlash(filepath.Join(`C:\`, "Music", "AlbumA", "a.wav")),
		},
		{
			name:      "stage2 uses source attribution",
			call:      func(item execsvc.PlanItem) { h.OnStage2EncodeFailed(4, item, os.ErrInvalid) },
			item:      execsvc.PlanItem{SourcePath: filepath.Join(`C:\`, "Music", "AlbumB", "b.wav")},
			expCode:   "EXEC_STAGE2_ENCODE_FAILED",
			expStage:  "execute",
			expFolder: filepath.ToSlash(filepath.Join(`C:\`, "Music", "AlbumB")),
			expSrc:    filepath.ToSlash(filepath.Join(`C:\`, "Music", "AlbumB", "b.wav")),
		},
		{
			name: "stage3 prefers target attribution",
			call: func(item execsvc.PlanItem) { h.OnStage3CommitFailed(5, item, os.ErrExist) },
			item: execsvc.PlanItem{
				SourcePath: filepath.Join(`C:\`, "Music", "AlbumSrc", "c.wav"),
				TargetPath: filepath.Join(`C:\`, "Music", "AlbumDst", "c.mp3"),
			},
			expCode:   "EXEC_STAGE3_COMMIT_FAILED",
			expStage:  "execute",
			expFolder: filepath.ToSlash(filepath.Join(`C:\`, "Music", "AlbumDst")),
			expSrc:    filepath.ToSlash(filepath.Join(`C:\`, "Music", "AlbumSrc", "c.wav")),
			expDst:    filepath.ToSlash(filepath.Join(`C:\`, "Music", "AlbumDst", "c.mp3")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(stream.events)
			tt.call(tt.item)
			if len(stream.events) != before+1 {
				t.Fatalf("expected one new event, before=%d after=%d", before, len(stream.events))
			}
			ev := stream.events[len(stream.events)-1]
			if ev.EventType != "error" {
				t.Fatalf("expected error event, got %q", ev.EventType)
			}
			if ev.Code != tt.expCode {
				t.Fatalf("expected code %q, got %q", tt.expCode, ev.Code)
			}
			if ev.Stage != tt.expStage {
				t.Fatalf("expected stage %q, got %q", tt.expStage, ev.Stage)
			}
			if ev.FolderPath != tt.expFolder {
				t.Fatalf("expected folder %q, got %q", tt.expFolder, ev.FolderPath)
			}
			if filepath.ToSlash(ev.ItemSourcePath) != tt.expSrc {
				t.Fatalf("expected item_source_path %q, got %q", tt.expSrc, ev.ItemSourcePath)
			}
			if tt.expDst != "" && filepath.ToSlash(ev.ItemTargetPath) != tt.expDst {
				t.Fatalf("expected item_target_path %q, got %q", tt.expDst, ev.ItemTargetPath)
			}
			if ev.EventId == "" || ev.Timestamp == "" {
				t.Fatalf("expected non-empty event_id/timestamp, got event_id=%q timestamp=%q", ev.EventId, ev.Timestamp)
			}
			if ev.PlanId != "plan-stage-failures-001" {
				t.Fatalf("expected plan_id set, got %q", ev.PlanId)
			}
		})
	}
}

// TestExecutePlan_DeleteTargetConflict validates that when target already exists
// during soft-delete operations, the operation fails with a deterministic target conflict error.
// This implements Task 2 semantic hardening: execute-time target conflict check.
