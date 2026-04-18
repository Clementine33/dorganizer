package grpc

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestPlanOperations_ErrorEventPersisted_PlanError verifies that plan-stage
// errors are persisted into the error_events table.
func TestPlanOperations_ErrorEventPersisted_PlanError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-plan-error-persist-*")
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

	// Create folder with a null-byte entry to trigger a PATH_NULL_BYTE error
	invalidFolder := filepath.Join(tmpDir, "bad_folder")
	if err := os.MkdirAll(invalidFolder, 0755); err != nil {
		t.Fatal(err)
	}

	invalidPath := filepath.ToSlash(invalidFolder) + "/test\u0000song.mp3"
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, invalidPath, filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatal(err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	req := &pb.PlanOperationsRequest{
		FolderPaths: []string{invalidFolder},
		PlanType:    "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify the response contains plan errors
	if len(resp.GetPlanErrors()) == 0 {
		t.Fatal("expected plan_errors in response")
	}

	// Verify error_events table has a matching row
	events, err := repo.ListErrorEventsByRoot(filepath.ToSlash(tmpDir))
	if err != nil {
		t.Fatalf("failed to list error events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one error_event row persisted for plan error")
	}

	// Verify the persisted event fields match
	found := false
	for _, e := range events {
		if e.Code == "PATH_NULL_BYTE" {
			found = true
			if e.Scope != "slim" {
				t.Errorf("expected scope='slim', got %q", e.Scope)
			}
			if e.RootPath == "" {
				t.Error("expected non-empty root_path")
			}
			if e.Message == "" {
				t.Error("expected non-empty message")
			}
			if e.Retryable {
				t.Error("expected retryable=false for plan error")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected PATH_NULL_BYTE error in error_events, got: %+v", events)
	}
}

// TestExecutePlan_ErrorEventPersisted_PreconditionFailed verifies that
// execute-stage errors are persisted into the error_events table.
func TestExecutePlan_ErrorEventPersisted_PreconditionFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-exec-error-persist-*")
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
	planID := "plan-exec-error-persist-001"
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

	// Execute plan (will fail due to stale precondition)
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	// Verify error_events table has a matching row
	events, err := repo.ListErrorEventsByRoot(filepath.ToSlash(tmpDir))
	if err != nil {
		t.Fatalf("failed to list error events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one error_event row persisted for execute error")
	}

	// Verify the persisted event fields match
	found := false
	for _, e := range events {
		if e.Code == "EXEC_PRECONDITION_FAILED" {
			found = true
			if e.Scope != "execute" {
				t.Errorf("expected scope='execute', got %q", e.Scope)
			}
			if e.RootPath == "" {
				t.Error("expected non-empty root_path")
			}
			if e.Path == nil || *e.Path == "" {
				t.Error("expected non-empty path for folder-attributed execute error")
			}
			if e.Message == "" {
				t.Error("expected non-empty message")
			}
			if e.Retryable {
				t.Error("expected retryable=false for precondition failed")
			}
			break
		}
	}
	if !found {
		t.Errorf("expected EXEC_PRECONDITION_FAILED in error_events, got: %+v", events)
	}
}

// findErrorEventByCode queries error_events directly for a given code, returning
// the first match. Used for cases where rootPath may be empty (ListErrorEventsByRoot
// won't return those rows).
func findErrorEventByCode(t *testing.T, repo *sqlite.Repository, code string) *sqlite.ErrorEvent {
	t.Helper()
	row := repo.DB().QueryRow(
		`SELECT id, scope, root_path, path, code, message, retryable, created_at FROM error_events WHERE code = ? ORDER BY id DESC LIMIT 1`,
		code,
	)
	var e sqlite.ErrorEvent
	var retryable int
	var createdAtStr string
	var path sql.NullString
	if err := row.Scan(&e.ID, &e.Scope, &e.RootPath, &path, &e.Code, &e.Message, &retryable, &createdAtStr); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		t.Fatalf("query error_events by code %q: %v", code, err)
	}
	if path.Valid {
		e.Path = &path.String
	}
	e.Retryable = retryable == 1
	return &e
}

// TestExecutePlan_ErrorEventPersisted_InvalidPlanID verifies that the
// INVALID_PLAN_ID RPC-level error is persisted to error_events.
func TestExecutePlan_ErrorEventPersisted_InvalidPlanID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-exec-invalid-planid-*")
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

	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: ""}
	_ = server.ExecutePlan(req, stream)

	// Verify error_events has a row for INVALID_PLAN_ID
	ev := findErrorEventByCode(t, repo, "INVALID_PLAN_ID")
	if ev == nil {
		t.Fatal("expected INVALID_PLAN_ID row in error_events")
	}
	if ev.Scope != "execute" {
		t.Errorf("expected scope='execute', got %q", ev.Scope)
	}
	if ev.Message == "" {
		t.Error("expected non-empty message")
	}
	if ev.Retryable {
		t.Error("expected retryable=false")
	}
}

// TestExecutePlan_ErrorEventPersisted_PlanNotFound verifies that the
// PLAN_NOT_FOUND RPC-level error is persisted to error_events.
func TestExecutePlan_ErrorEventPersisted_PlanNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-exec-plan-notfound-*")
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

	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: "nonexistent-plan-id"}
	_ = server.ExecutePlan(req, stream)

	// Verify error_events has a row for PLAN_NOT_FOUND
	ev := findErrorEventByCode(t, repo, "PLAN_NOT_FOUND")
	if ev == nil {
		t.Fatal("expected PLAN_NOT_FOUND row in error_events")
	}
	if ev.Scope != "execute" {
		t.Errorf("expected scope='execute', got %q", ev.Scope)
	}
	if ev.Message == "" {
		t.Error("expected non-empty message")
	}
	if ev.Retryable {
		t.Error("expected retryable=false")
	}
}

// TestPlanOperations_ErrorEventPersisted_GlobalNoScope verifies that the
// GLOBAL_NO_SCOPE plan-level error is persisted to error_events.
func TestPlanOperations_ErrorEventPersisted_GlobalNoScope(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-plan-global-noscope-*")
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

	// Request with no scope at all — triggers GLOBAL_NO_SCOPE
	req := &pb.PlanOperationsRequest{
		PlanType: "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify the response contains the global error
	if resp.GetSummaryReason() != "GLOBAL_SHORT_CIRCUIT" {
		t.Fatalf("expected summary_reason='GLOBAL_SHORT_CIRCUIT', got %q", resp.GetSummaryReason())
	}

	// Verify error_events has a row for GLOBAL_NO_SCOPE
	ev := findErrorEventByCode(t, repo, "GLOBAL_NO_SCOPE")
	if ev == nil {
		t.Fatal("expected GLOBAL_NO_SCOPE row in error_events")
	}
	if ev.Scope != "plan" {
		t.Errorf("expected scope='plan', got %q", ev.Scope)
	}
	if ev.Message == "" {
		t.Error("expected non-empty message")
	}
	if ev.Retryable {
		t.Error("expected retryable=false")
	}
}
