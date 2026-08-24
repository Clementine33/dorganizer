package grpc

import (
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutePlan_FolderCompleted_EmittedOnSuccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-folder-completed-*")
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
	planID := "plan-folder-completed-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with correct preconditions
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	if err != nil {
		t.Fatalf("ExecutePlan should succeed, got: %v", err)
	}

	// Find folder_completed event
	var folderCompletedEvent *pb.JobEvent
	for _, ev := range stream.events {
		if ev.EventType == "folder_completed" {
			folderCompletedEvent = ev
			break
		}
	}

	if folderCompletedEvent == nil {
		t.Fatal("expected folder_completed JobEvent to be sent for successful folder")
	}

	// Verify required fields
	if folderCompletedEvent.Stage != "execute" {
		t.Errorf("expected stage=execute, got %q", folderCompletedEvent.Stage)
	}
	if folderCompletedEvent.FolderPath == "" {
		t.Error("expected folder_path to be populated for folder_completed")
	}
	if folderCompletedEvent.PlanId != planID {
		t.Errorf("expected plan_id=%s, got %q", planID, folderCompletedEvent.PlanId)
	}
	if folderCompletedEvent.RootPath == "" {
		t.Error("expected root_path to be populated")
	}
	if folderCompletedEvent.EventId == "" {
		t.Error("expected event_id to be populated")
	}
	if folderCompletedEvent.Timestamp == "" {
		t.Error("expected timestamp to be populated")
	}

	expectedMessage := "Scope " + folderCompletedEvent.FolderPath + " completed"
	if folderCompletedEvent.Message != expectedMessage {
		t.Errorf("expected message %q, got %q", expectedMessage, folderCompletedEvent.Message)
	}
}

// TestExecutePlan_FolderCompleted_NotEmittedOnFailure validates that folder_completed
// is NOT emitted for failed folders.

func TestExecutePlan_FolderCompleted_NotEmittedOnFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-no-folder-completed-*")
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
	planID := "plan-no-folder-completed-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with STALE precondition (will fail)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan (will fail due to stale precondition)
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	// Verify NO folder_completed event
	for _, ev := range stream.events {
		if ev.EventType == "folder_completed" {
			t.Error("folder_completed should NOT be emitted for failed folder")
		}
	}
}

// TestExecutePlan_FolderCompleted_EmittedBeforeLaterFolderFailure validates
// streaming behavior: when one folder completes and a later folder fails,
// folder_completed is emitted immediately (before the later error event).

func TestExecutePlan_FolderCompleted_EmittedBeforeLaterFolderFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-folder-completed-order-*")
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

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(folderA, "a.mp3")
	fileB := filepath.Join(folderB, "b.mp3")
	if err := os.WriteFile(fileA, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	infoA, err := os.Stat(fileA)
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := os.Stat(fileB)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileA), filepath.ToSlash(tmpDir), infoA.Size(), infoA.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileB), filepath.ToSlash(tmpDir), infoB.Size(), infoB.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-folder-completed-order-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatal(err)
	}

	// Folder A succeeds.
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(fileA), filepath.ToSlash(fileA), infoA.Size(), infoA.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	// Folder B fails precondition.
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 1, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(fileB), filepath.ToSlash(fileB), infoB.Size(), infoB.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}

	if err := server.ExecutePlan(req, stream); err != nil {
		t.Fatalf("ExecutePlan should continue rooted execution, got: %v", err)
	}

	folderCompletedIdx := -1
	errorForFolderBIdx := -1
	folderBPath := filepath.ToSlash(folderB)
	for i, ev := range stream.events {
		if ev.EventType == "folder_completed" && strings.Contains(ev.FolderPath, "AlbumA") {
			folderCompletedIdx = i
		}
		if ev.EventType == "error" && ev.Code == "EXEC_PRECONDITION_FAILED" && ev.FolderPath == folderBPath {
			errorForFolderBIdx = i
		}
	}

	if folderCompletedIdx == -1 {
		t.Fatal("expected folder_completed for AlbumA")
	}
	if errorForFolderBIdx == -1 {
		t.Fatal("expected EXEC_PRECONDITION_FAILED error for AlbumB")
	}
	if folderCompletedIdx > errorForFolderBIdx {
		t.Fatalf("expected folder_completed for AlbumA to be emitted before AlbumB error, got completed_idx=%d error_idx=%d", folderCompletedIdx, errorForFolderBIdx)
	}
}

// TestExecutePlan_FolderFailed_EmittedOnFailure validates that folder_failed
// event is emitted for failed folders with required fields and message.

func TestExecutePlan_FolderFailed_EmittedOnFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-folder-failed-*")
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
	planID := "plan-folder-failed-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with STALE precondition (will fail)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	var folderFailedEvent *pb.JobEvent
	folderFailedCount := 0
	for _, ev := range stream.events {
		if ev.EventType == "folder_failed" {
			folderFailedCount++
			folderFailedEvent = ev
		}
	}

	if folderFailedEvent == nil {
		t.Fatal("expected folder_failed JobEvent to be sent for failed folder")
	}
	if folderFailedCount != 1 {
		t.Fatalf("expected exactly 1 folder_failed event, got %d", folderFailedCount)
	}

	if folderFailedEvent.Stage != "execute" {
		t.Errorf("expected stage=execute, got %q", folderFailedEvent.Stage)
	}
	if folderFailedEvent.FolderPath == "" {
		t.Error("expected folder_path to be populated for folder_failed")
	}
	if folderFailedEvent.PlanId != planID {
		t.Errorf("expected plan_id=%s, got %q", planID, folderFailedEvent.PlanId)
	}
	if folderFailedEvent.RootPath == "" {
		t.Error("expected root_path to be populated")
	}
	if folderFailedEvent.EventId == "" {
		t.Error("expected event_id to be populated")
	}
	if folderFailedEvent.Timestamp == "" {
		t.Error("expected timestamp to be populated")
	}

	expectedMessage := "Scope " + folderFailedEvent.FolderPath + " failed"
	if folderFailedEvent.Message != expectedMessage {
		t.Errorf("expected message %q, got %q", expectedMessage, folderFailedEvent.Message)
	}
}

// TestExecutePlan_ConfigInvalid_RemainsGlobal validates that CONFIG_INVALID errors
// remain global (folder_path="") and don't become folder-attributed.

func TestExecutePlan_ConfigInvalid_RemainsGlobal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-config-invalid-*")
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

	// Create config.json with NO tools config (empty encoder)
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"prune": { "regex_pattern": "^\\." },
		"tools": {}
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.wav")
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
		VALUES (?, ?, 0, ?, 'audio/wav', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan with CONVERT operation (requires tools config)
	planID := "plan-config-invalid-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_convert', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with convert operation
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'convert_and_delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(filepath.Join(tmpDir, "test.mp3")), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server with config that has no tools
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

	// Verify CONFIG_INVALID error is GLOBAL (folder_path="")
	if errorEvent.Code == "CONFIG_INVALID" && errorEvent.FolderPath != "" {
		t.Errorf("CONFIG_INVALID error should be global (folder_path=''), got %q", errorEvent.FolderPath)
	}
}

// TestExecutePlan_InvalidPlanId_RemainsGlobal validates that invalid plan_id
// errors remain global.

func TestExecutePlan_InvalidPlanId_RemainsGlobal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-invalid-planid-*")
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

	// Execute with empty plan_id
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: ""}
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

	// Empty plan_id error should be global
	if errorEvent.FolderPath != "" {
		t.Errorf("invalid plan_id error should be global (folder_path=''), got %q", errorEvent.FolderPath)
	}
}

// TestExecutePlan_PlanNotFound_RemainsGlobal validates that plan not found
// errors remain global.

func TestExecutePlan_PlanNotFound_RemainsGlobal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-plan-notfound-*")
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

	// Execute with non-existent plan_id
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: "non-existent-plan-id"}
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

	// Plan not found error should be global
	if errorEvent.FolderPath != "" {
		t.Errorf("plan not found error should be global (folder_path=''), got %q", errorEvent.FolderPath)
	}
}

// TestExecutePlan_EventId_Unique validates that different logical events
// have unique event_ids.

func TestExecutePlan_EventId_Unique(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-eventid-unique-*")
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

	// Create test audio files in two subfolders
	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(folderA, "test.mp3")
	fileB := filepath.Join(folderB, "test.mp3")
	if err := os.WriteFile(fileA, []byte("dummy a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("dummy b"), 0644); err != nil {
		t.Fatal(err)
	}

	infoA, _ := os.Stat(fileA)
	infoB, _ := os.Stat(fileB)

	// Insert entries into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileA), filepath.ToSlash(tmpDir), infoA.Size(), infoA.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileB), filepath.ToSlash(tmpDir), infoB.Size(), infoB.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	// Create plan
	planID := "plan-eventid-unique-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatal(err)
	}

	// Create plan items with stale preconditions (both will fail)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(fileA), filepath.ToSlash(fileA), infoA.Size(), infoA.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 1, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(fileB), filepath.ToSlash(fileB), infoB.Size(), infoB.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	// Collect all event_ids from events
	eventIds := make(map[string]int)
	for _, ev := range stream.events {
		if ev.EventId != "" {
			eventIds[ev.EventId]++
		}
	}

	// Verify each event_id appears only once
	for id, count := range eventIds {
		if count > 1 {
			t.Errorf("event_id %q appears %d times, expected unique per logical event", id, count)
		}
	}
}

// TestExecutePlan_AttributionPrecedence_Precondition validates attribution precedence
// for precondition failures: item_source_path > precondition_path > item_target_path

func TestExecutePlan_PerFolderFailFast(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-perfolder-failfast-*")
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

	// Create two folders with files
	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	// Folder A: 2 files (first will fail, second should NOT be processed)
	fileA1 := filepath.Join(folderA, "song1.mp3")
	fileA2 := filepath.Join(folderA, "song2.mp3")
	if err := os.WriteFile(fileA1, []byte("a1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileA2, []byte("a2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Folder B: 1 file (should be processed even if Folder A fails)
	fileB1 := filepath.Join(folderB, "song1.mp3")
	if err := os.WriteFile(fileB1, []byte("b1"), 0644); err != nil {
		t.Fatal(err)
	}

	infoA1, _ := os.Stat(fileA1)
	infoA2, _ := os.Stat(fileA2)
	infoB1, _ := os.Stat(fileB1)

	// Insert entries into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileA1), filepath.ToSlash(tmpDir), infoA1.Size(), infoA1.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileA2), filepath.ToSlash(tmpDir), infoA2.Size(), infoA2.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(fileB1), filepath.ToSlash(tmpDir), infoB1.Size(), infoB1.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	// Create plan
	planID := "plan-perfolder-failfast-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatal(err)
	}

	// Create plan items:
	// - A1: stale precondition (will fail)
	// - A2: valid precondition (should NOT be processed due to fail-fast in folder A)
	// - B1: valid precondition (should be processed)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 999, ?, ?)
	`, planID, filepath.ToSlash(fileA1), filepath.ToSlash(fileA1), infoA1.Size(), infoA1.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 1, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(fileA2), filepath.ToSlash(fileA2), infoA2.Size(), infoA2.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 2, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(fileB1), filepath.ToSlash(fileB1), infoB1.Size(), infoB1.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	// Verify:
	// 1. A1 file still exists (precondition failed, no mutation)
	if _, err := os.Stat(fileA1); os.IsNotExist(err) {
		t.Error("A1 should still exist (precondition failure blocks mutation)")
	}
	// 2. A2 file still exists (fail-fast should have stopped folder A processing)
	if _, err := os.Stat(fileA2); os.IsNotExist(err) {
		t.Error("A2 should still exist (fail-fast should have stopped folder A)")
	}
	// 3. B1 file should be deleted (folder B continues after folder A fails)
	if _, err := os.Stat(fileB1); !os.IsNotExist(err) {
		t.Error("B1 should be deleted (folder B should continue)")
	}

	// 4. Verify folder_completed was sent for folder B but NOT for folder A
	folderCompletedCount := 0
	for _, ev := range stream.events {
		if ev.EventType == "folder_completed" {
			folderCompletedCount++
			// Should only be for folder B
			if !strings.Contains(ev.FolderPath, "AlbumB") {
				t.Errorf("folder_completed should only be for AlbumB, got %q", ev.FolderPath)
			}
		}
	}
	if folderCompletedCount != 1 {
		t.Errorf("expected 1 folder_completed event (for AlbumB), got %d", folderCompletedCount)
	}
}

// TestExecutePlan_Delete_UsesPersistedTargetPath validates that delete operations
// use the persisted target_path verbatim, not recomputed at execution time.
