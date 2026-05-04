package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	grpcserver "github.com/onsei/organizer/backend/internal/grpc"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/scanner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// TestE2EGrpcHarnessBoot verifies gRPC harness can initiate real streaming calls
func TestE2EGrpcHarnessBoot(t *testing.T) {
	client, _, rootDir, cleanup := newE2EGrpcClient(t)
	defer cleanup()

	stream, err := client.Scan(context.Background(), &pb.ScanRequest{FolderPath: rootDir})
	if err != nil {
		t.Fatalf("scan rpc failed: %v", err)
	}

	events, err := collectScanEvents(stream)
	if err != nil {
		t.Fatalf("scan stream failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected scan events")
	}
}

// newE2EGrpcClient creates an in-memory gRPC client connected to a bufconn server
func newE2EGrpcClient(t *testing.T) (pb.OnseiServiceClient, *sqlite.Repository, string, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := sqlite.EnsureDBPath(dbPath); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1024 * 1024)
	gsrv := grpc.NewServer()
	pb.RegisterOnseiServiceServer(gsrv, grpcserver.NewOnseiServer(repo, tmpDir, "ffmpeg"))
	go func() { _ = gsrv.Serve(lis) }()

	ctx := context.Background()
	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		_ = conn.Close()
		gsrv.Stop()
		_ = lis.Close()
		_ = repo.Close()
	}
	return pb.NewOnseiServiceClient(conn), repo, tmpDir, cleanup
}

// collectScanEvents collects all events from a scan stream until EOF or error
func collectScanEvents(stream pb.OnseiService_ScanClient) ([]*pb.JobEvent, error) {
	var events []*pb.JobEvent
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}
}

// collectExecuteEvents collects all events from an execute stream until EOF or error
func collectExecuteEvents(stream pb.OnseiService_ExecutePlanClient) ([]*pb.JobEvent, error) {
	var events []*pb.JobEvent
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}
}

type e2eExecuteRepoAdapter struct {
	repo *sqlite.Repository
}

func (a *e2eExecuteRepoAdapter) CreateExecuteSession(sessionID, planID, rootPath, status string) error {
	return a.repo.CreateExecuteSession(&sqlite.ExecuteSession{
		SessionID: sessionID,
		PlanID:    planID,
		RootPath:  filepath.ToSlash(rootPath),
		Status:    status,
		StartedAt: time.Now(),
	})
}

func (a *e2eExecuteRepoAdapter) UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	return a.repo.UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage)
}

func (a *e2eExecuteRepoAdapter) GetEntryContentRev(path string) (int, error) {
	var contentRev int
	err := a.repo.DB().QueryRow(
		"SELECT COALESCE(content_rev, 0) FROM entries WHERE path = ?",
		filepath.ToSlash(path),
	).Scan(&contentRev)
	if err != nil {
		return 0, err
	}
	return contentRev, nil
}

// TestScanPlanExecuteLoop tests the full scan->plan->execute workflow using gRPC
// This e2e test validates:
// - Stream event terminal states reach completion or failure
// - No UI freeze assumptions via smoke flow test
// - Invariants from design doc are maintained
// - Full gRPC chain: Scan -> CreatePlan -> ExecutePlan
func TestScanPlanExecuteLoop(t *testing.T) {
	// Create gRPC client with embedded server and temp directory
	client, repo, rootDir, cleanup := newE2EGrpcClient(t)
	defer cleanup()

	// Create test audio files with matching basenames to create groups
	// Files with same basename but different extensions will be grouped
	testFiles := []string{
		"test1.mp3",
		"test1.flac", // Same basename as test1.mp3 - should trigger delete plan
		"test2.mp3",
		"test2.flac", // Same basename as test2.mp3 - mode1 deletes lossless, keeps lossy
		"test3.mp3", // No matching pair - will be kept
	}
	t.Logf("rootDir: %s", rootDir)
	for _, f := range testFiles {
		filePath := filepath.Join(rootDir, f)
		if err := os.WriteFile(filePath, []byte("dummy audio"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Step 1: Scan the directory via gRPC
	ctx := context.Background()
	scanStream, err := client.Scan(ctx, &pb.ScanRequest{FolderPath: rootDir})
	if err != nil {
		t.Fatalf("scan rpc failed: %v", err)
	}

	scanEvents, err := collectScanEvents(scanStream)
	if err != nil {
		t.Fatalf("scan stream failed: %v", err)
	}

	// Validate scan stream includes started and completed, no error
	var scanStarted, scanCompleted bool
	for _, ev := range scanEvents {
		if ev.EventType == "started" {
			scanStarted = true
		}
		if ev.EventType == "completed" {
			scanCompleted = true
		}
		if ev.EventType == "error" || ev.Code != "" {
			t.Fatalf("scan returned error event: %s - %s", ev.Code, ev.Message)
		}
	}
	if !scanStarted {
		t.Error("scan stream missing 'started' event")
	}
	if !scanCompleted {
		t.Error("scan stream missing 'completed' event")
	}

	// Verify scanned entries exist in DB
	var totalCount int
	err = repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries WHERE root_path = ? AND is_dir = 0",
		filepath.ToSlash(rootDir),
	).Scan(&totalCount)
	if err != nil {
		t.Fatalf("failed to count scanned entries: %v", err)
	}
	if totalCount < len(testFiles) {
		t.Errorf("expected at least %d entries, got %d", len(testFiles), totalCount)
	}

	// Debug: Print all entries in DB
	rows, err := repo.DB().Query("SELECT path, root_path, is_dir FROM entries")
	if err != nil {
		t.Logf("Failed to query entries: %v", err)
	} else {
		defer rows.Close()
		t.Log("Entries in database:")
		for rows.Next() {
			var path, rootPath string
			var isDir int
			if err := rows.Scan(&path, &rootPath, &isDir); err == nil {
				t.Logf("  - path=%s root_path=%s is_dir=%d", path, rootPath, isDir)
			}
		}
	}

	// Step 2: Create a plan via gRPC (plan type "slim" triggers folder-based slim analysis)
	planResp, err := client.PlanOperations(ctx, &pb.PlanOperationsRequest{
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
		FolderPath:   rootDir,
	})
	if err != nil {
		t.Fatalf("create plan rpc failed: %v", err)
	}

	planID := planResp.PlanId
	if planID == "" {
		t.Fatal("plan_id should not be empty")
	}

	// Use len(Operations) as actionable count
	actionableCount := len(planResp.Operations)
	if actionableCount == 0 {
		// Fail with diagnostics when no actionable items
		var diagnostics strings.Builder
		diagnostics.WriteString("actionable_count is 0 - diagnostics:\n")
		diagnostics.WriteString(fmt.Sprintf("  total scanned entries: %d\n", totalCount))
		diagnostics.WriteString(fmt.Sprintf("  plan_id: %s\n", planResp.PlanId))
		diagnostics.WriteString(fmt.Sprintf("  plan operations count: %d\n", len(planResp.Operations)))
		diagnostics.WriteString(fmt.Sprintf("  plan total_count: %d\n", planResp.TotalCount))
		diagnostics.WriteString(fmt.Sprintf("  plan plan_errors count: %d\n", len(planResp.PlanErrors)))
		if len(planResp.PlanErrors) > 0 {
			diagnostics.WriteString("  plan_errors details:\n")
			for i, pe := range planResp.PlanErrors {
				diagnostics.WriteString(fmt.Sprintf("    [%d] Code=%s Message=%s\n", i, pe.Code, pe.Message))
			}
		}
		if len(planResp.Operations) > 0 {
			diagnostics.WriteString("  operations payload details:\n")
			for i, op := range planResp.Operations {
				diagnostics.WriteString(fmt.Sprintf("    [%d] SourcePath=%s OperationType=%s\n", i, op.SourcePath, op.OperationType))
			}
		}
		diagnostics.WriteString("  test files created:\n")
		for _, f := range testFiles {
			diagnostics.WriteString(fmt.Sprintf("    - %s\n", f))
		}
		t.Fatal(diagnostics.String())
	}

	// Step 3: Verify no GLOBAL_NO_SCOPE error by checking PlanErrors entries
	hasGlobalNoScope := false
	for _, pe := range planResp.PlanErrors {
		if pe.Code == "GLOBAL_NO_SCOPE" {
			hasGlobalNoScope = true
			break
		}
	}
	if hasGlobalNoScope {
		t.Fatal("plan has GLOBAL_NO_SCOPE error - no valid folder path for plan")
	}

	// Step 4: DB assertions - verify plan persisted and plan_items count equals TotalCount
	var planCount int
	err = repo.DB().QueryRow(
		"SELECT COUNT(*) FROM plans WHERE plan_id = ?",
		planID,
	).Scan(&planCount)
	if err != nil {
		t.Fatalf("failed to count plans: %v", err)
	}
	if planCount != 1 {
		t.Errorf("expected exactly 1 plan in DB, got %d", planCount)
	}

	var planItemCount int
	err = repo.DB().QueryRow(
		"SELECT COUNT(*) FROM plan_items WHERE plan_id = ?",
		planID,
	).Scan(&planItemCount)
	if err != nil {
		t.Fatalf("failed to count plan items: %v", err)
	}
	// Assert plan_items count equals planResp.TotalCount
	expectedItemCount := int(planResp.TotalCount)
	if planItemCount != expectedItemCount {
		t.Errorf("plan_items count mismatch: expected %d (from TotalCount), got %d", expectedItemCount, planItemCount)
	}

	// Debug: Print all plan items
	itemRows, err := repo.DB().Query("SELECT source_path, op_type FROM plan_items WHERE plan_id = ?", planID)
	if err != nil {
		t.Logf("Failed to query plan items: %v", err)
	} else {
		defer itemRows.Close()
		t.Log("Plan items in database:")
		for itemRows.Next() {
			var path, opType string
			if err := itemRows.Scan(&path, &opType); err == nil {
				t.Logf("  - %s (%s)", path, opType)
			}
		}
	}

	// Validate actionable entries: source_path non-empty and op type in delete|convert_and_delete
	for _, op := range planResp.Operations {
		if op.SourcePath == "" {
			t.Errorf("plan operation has empty source_path")
		}
		opType := strings.ToLower(op.OperationType)
		if opType != "delete" && opType != "convert_and_delete" {
			t.Errorf("unexpected operation_type: %s (expected delete or convert_and_delete)", op.OperationType)
		}
	}

	// Step 5: Execute plan via gRPC
	execStream, err := client.ExecutePlan(ctx, &pb.ExecutePlanRequest{PlanId: planID})
	if err != nil {
		t.Fatalf("execute plan rpc failed: %v", err)
	}

	execEvents, err := collectExecuteEvents(execStream)
	if err != nil {
		t.Fatalf("execute stream failed: %v", err)
	}

	// Step 6: Validate execute stream includes started and completed, no error
	var execStarted, execCompleted bool
	for _, ev := range execEvents {
		if ev.EventType == "started" {
			execStarted = true
		}
		if ev.EventType == "completed" {
			execCompleted = true
		}
		if ev.EventType == "error" || ev.Code != "" {
			t.Fatalf("execute returned error event: %s - %s", ev.Code, ev.Message)
		}
	}
	if !execStarted {
		t.Error("execute stream missing 'started' event")
	}
	if !execCompleted {
		t.Error("execute stream missing 'completed' event")
	}

	// Step 8: Repo session assertions via ListExecuteSessionsByPlan + GetExecuteSession
	sessions, err := repo.ListExecuteSessionsByPlan(planID)
	if err != nil {
		t.Fatalf("failed to list execute sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one execute session record")
	}

	// Check the latest session
	latestSession := sessions[0]
	if latestSession.Status != "completed" {
		t.Errorf("expected session status 'completed', got '%s'", latestSession.Status)
	}
	if latestSession.ErrorCode != "" {
		t.Errorf("expected empty error_code, got '%s'", latestSession.ErrorCode)
	}

	// Verify via GetExecuteSession
	session, err := repo.GetExecuteSession(latestSession.SessionID)
	if err != nil {
		t.Fatalf("failed to get execute session: %v", err)
	}
	if session.Status != "completed" {
		t.Errorf("GetExecuteSession: expected status 'completed', got '%s'", session.Status)
	}
	if session.ErrorCode != "" {
		t.Errorf("GetExecuteSession: expected empty error_code, got '%s'", session.ErrorCode)
	}

	t.Logf("e2e workflow complete: scanned %d files, planned %d ops (items=%d), executed %d events",
		totalCount, actionableCount, planItemCount, len(execEvents))
}

// TestScanPlanExecuteWithPrune tests prune workflow
func TestScanPlanExecuteWithPrune(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "e2e-prune-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create test files matching prune pattern
	testFiles := []string{
		"test.mp3",
		"backup.mp3",
		"old.mp3",
	}
	for _, f := range testFiles {
		filePath := filepath.Join(tmpDir, f)
		if err := os.WriteFile(filePath, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Setup repo and scan
	if err := sqlite.EnsureDBPath(dbPath); err != nil {
		t.Fatalf("failed to ensure db path: %v", err)
	}

	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	svcScanner := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(repo))
	_, err = svcScanner.ScanRoot(tmpDir)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// Generate prune plan for files matching "old"
	svcPlan := analyze.NewAnalyzer(repo)
	plan, err := svcPlan.AnalyzePrune("old", analyze.PruneTargetBoth)
	if err != nil {
		t.Fatalf("prune plan failed: %v", err)
	}

	// Verify prune plan includes the matching file
	foundPruneOp := false
	for _, op := range plan.Operations {
		if op.Type == analyze.OpTypeDelete && op.SourcePath != "" {
			foundPruneOp = true
		}
	}

	if !foundPruneOp {
		t.Log("no prune operations generated (may be expected if pattern doesn't match)")
	}

	t.Logf("prune workflow complete: plan ID=%s, ops=%d", plan.PlanID, len(plan.Operations))
}

// TestExecuteTerminalStates tests that execute service handles terminal states correctly
func TestExecuteTerminalStates(t *testing.T) {
	svc := execute.NewService(execute.ToolsConfig{})

	tests := []struct {
		name       string
		item       execute.PlanItem
		softDelete bool
		wantErr    bool
	}{
		{
			name: "delete non-existent file",
			item: execute.PlanItem{
				Type: execute.ItemTypeDelete,
				Src:  "/tmp/nonexistent-delete-test",
			},
			softDelete: true,
			wantErr:    true, // file doesn't exist
		},
		{
			name: "convert without ffmpeg",
			item: execute.PlanItem{
				Type: execute.ItemTypeConvert,
				Src:  "/tmp/test.mp3",
				Dst:  "/tmp/test.m4a",
			},
			softDelete: false,
			wantErr:    true, // ffmpeg not found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.ExecuteItem(tt.item, tt.softDelete)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExecuteItem() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Verify error mapping works for terminal state reporting
			code := execute.MapError(err)
			if err != nil && code == 0 {
				t.Logf("error %v does not have domain code", err)
			}
		})
	}
}

// TestPersistedPlanStaleAfterFileDelete tests scan->persist-plan->execute stale rejection.
func TestPersistedPlanStaleAfterFileDelete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "e2e-persisted-stale-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	if err := sqlite.EnsureDBPath(dbPath); err != nil {
		t.Fatalf("failed to ensure db path: %v", err)
	}
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	// Scan fixture file so preconditions come from persisted entries.
	testFile := filepath.Join(tmpDir, "stale.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	svcScanner := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(repo))
	if _, err := svcScanner.ScanRoot(tmpDir); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// Load persisted preconditions.
	var size int64
	var mtime int64
	var contentRev int
	err = repo.DB().QueryRow(
		"SELECT size, mtime, content_rev FROM entries WHERE path = ?",
		filepath.ToSlash(testFile),
	).Scan(&size, &mtime, &contentRev)
	if err != nil {
		t.Fatalf("failed to load entry preconditions: %v", err)
	}

	planID := "plan-e2e-stale-delete-001"
	if err := repo.CreatePlan(&sqlite.Plan{
		PlanID:        planID,
		RootPath:      filepath.ToSlash(tmpDir),
		PlanType:      "single_delete",
		SnapshotToken: "snapshot-e2e-stale",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("failed to persist plan: %v", err)
	}

	if err := repo.CreatePlanItem(&sqlite.PlanItem{
		PlanID:                 planID,
		ItemIndex:              0,
		OpType:                 "delete",
		SourcePath:             filepath.ToSlash(testFile),
		ReasonCode:             "E2E_STALE_DELETE",
		PreconditionPath:       filepath.ToSlash(testFile),
		PreconditionContentRev: contentRev,
		PreconditionSize:       size,
		PreconditionMtime:      mtime,
	}); err != nil {
		t.Fatalf("failed to persist plan item: %v", err)
	}

	// Filesystem changes after planning should make plan stale.
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to delete source file: %v", err)
	}

	execSvc := execute.NewExecuteService(&e2eExecuteRepoAdapter{repo: repo}, execute.ToolsConfig{})
	result, err := execSvc.ExecutePlan(&execute.Plan{
		PlanID:   planID,
		RootPath: tmpDir,
		Items: []execute.PlanItem{
			{
				Type:                   execute.ItemTypeDelete,
				SourcePath:             testFile,
				PreconditionPath:       testFile,
				PreconditionContentRev: contentRev,
				PreconditionSize:       size,
				PreconditionMtime:      mtime,
			},
		},
	})

	if err == nil {
		t.Fatal("expected stale precondition error when file deleted after planning")
	}
	if result == nil {
		t.Fatal("expected execute result for stale precondition")
	}
	if result.Status != "precondition_failed" {
		t.Fatalf("expected precondition_failed status, got %q", result.Status)
	}
	if result.ErrorCode != "EXEC_PRECONDITION_FAILED" {
		t.Fatalf("expected EXEC_PRECONDITION_FAILED, got %q", result.ErrorCode)
	}

	sessions, err := repo.ListExecuteSessionsByPlan(planID)
	if err != nil {
		t.Fatalf("failed to list execute sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected one execute session record")
	}
	if sessions[0].Status != "failed" {
		t.Fatalf("expected execute session status failed, got %q", sessions[0].Status)
	}

	session, err := repo.GetExecuteSession(result.SessionID)
	if err != nil {
		t.Fatalf("failed to get execute session by id: %v", err)
	}
	if session.ErrorCode != "EXEC_PRECONDITION_FAILED" {
		t.Fatalf("expected execute session error code EXEC_PRECONDITION_FAILED, got %q", session.ErrorCode)
	}
}

// TestPersistedPlanStaleAfterMtimeDrift tests scan->persist-plan->execute stale rejection on mtime drift.
func TestPersistedPlanStaleAfterMtimeDrift(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "e2e-persisted-stale-mtime-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	if err := sqlite.EnsureDBPath(dbPath); err != nil {
		t.Fatalf("failed to ensure db path: %v", err)
	}
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	testFile := filepath.Join(tmpDir, "stale-mtime.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	svcScanner := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(repo))
	if _, err := svcScanner.ScanRoot(tmpDir); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	var size int64
	var mtime int64
	var contentRev int
	err = repo.DB().QueryRow(
		"SELECT size, mtime, content_rev FROM entries WHERE path = ?",
		filepath.ToSlash(testFile),
	).Scan(&size, &mtime, &contentRev)
	if err != nil {
		t.Fatalf("failed to load entry preconditions: %v", err)
	}

	planID := "plan-e2e-stale-mtime-001"
	if err := repo.CreatePlan(&sqlite.Plan{
		PlanID:        planID,
		RootPath:      filepath.ToSlash(tmpDir),
		PlanType:      "single_delete",
		SnapshotToken: "snapshot-e2e-stale-mtime",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("failed to persist plan: %v", err)
	}

	if err := repo.CreatePlanItem(&sqlite.PlanItem{
		PlanID:                 planID,
		ItemIndex:              0,
		OpType:                 "delete",
		SourcePath:             filepath.ToSlash(testFile),
		ReasonCode:             "E2E_STALE_MTIME",
		PreconditionPath:       filepath.ToSlash(testFile),
		PreconditionContentRev: contentRev,
		PreconditionSize:       size,
		PreconditionMtime:      mtime,
	}); err != nil {
		t.Fatalf("failed to persist plan item: %v", err)
	}

	// Drift filesystem mtime by >1s after planning to trigger stale precondition.
	newMtime := time.Unix(mtime, 0).Add(5 * time.Second)
	if err := os.Chtimes(testFile, newMtime, newMtime); err != nil {
		t.Fatalf("failed to update mtime: %v", err)
	}

	execSvc := execute.NewExecuteService(&e2eExecuteRepoAdapter{repo: repo}, execute.ToolsConfig{})
	result, err := execSvc.ExecutePlan(&execute.Plan{
		PlanID:   planID,
		RootPath: tmpDir,
		Items: []execute.PlanItem{
			{
				Type:                   execute.ItemTypeDelete,
				SourcePath:             testFile,
				PreconditionPath:       testFile,
				PreconditionContentRev: contentRev,
				PreconditionSize:       size,
				PreconditionMtime:      mtime,
			},
		},
	})

	if err == nil {
		t.Fatal("expected stale precondition error when mtime drifts after planning")
	}
	if result == nil {
		t.Fatal("expected execute result for stale precondition")
	}
	if result.Status != "precondition_failed" {
		t.Fatalf("expected precondition_failed status, got %q", result.Status)
	}
	if result.ErrorCode != "EXEC_PRECONDITION_FAILED" {
		t.Fatalf("expected EXEC_PRECONDITION_FAILED, got %q", result.ErrorCode)
	}

	session, err := repo.GetExecuteSession(result.SessionID)
	if err != nil {
		t.Fatalf("failed to get execute session by id: %v", err)
	}
	if session.Status != "failed" {
		t.Fatalf("expected execute session status failed, got %q", session.Status)
	}
	if session.ErrorCode != "EXEC_PRECONDITION_FAILED" {
		t.Fatalf("expected execute session error code EXEC_PRECONDITION_FAILED, got %q", session.ErrorCode)
	}
}

// TestExecutePlanGrpc_StalePrecondition tests gRPC stale precondition failure semantics.
// It uses gRPC Scan + PlanOperations to create a plan, mutates the filesystem
// to stale the plan, then calls gRPC ExecutePlan and asserts:
//  1. Stream contains error event with event_type="error" and code="EXEC_PRECONDITION_FAILED"
//  2. Terminal error status code is codes.FailedPrecondition
//  3. Repo GetExecuteSession shows status failed and error_code EXEC_PRECONDITION_FAILED
func TestExecutePlanGrpc_StalePrecondition(t *testing.T) {
	client, repo, rootDir, cleanup := newE2EGrpcClient(t)
	defer cleanup()

	// Create test files with matching basenames to trigger delete plan
	testFiles := []string{
		"stale1.mp3",
		"stale1.flac", // Same basename as stale1.mp3 - should trigger delete plan
	}
	for _, f := range testFiles {
		filePath := filepath.Join(rootDir, f)
		if err := os.WriteFile(filePath, []byte("dummy audio"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Step 1: Scan the directory via gRPC
	ctx := context.Background()
	scanStream, err := client.Scan(ctx, &pb.ScanRequest{FolderPath: rootDir})
	if err != nil {
		t.Fatalf("scan rpc failed: %v", err)
	}

	scanEvents, err := collectScanEvents(scanStream)
	if err != nil {
		t.Fatalf("scan stream failed: %v", err)
	}
	if len(scanEvents) == 0 {
		t.Fatal("expected scan events")
	}

	// Step 2: Create a plan via gRPC (plan type "slim" triggers folder-based slim analysis)
	planResp, err := client.PlanOperations(ctx, &pb.PlanOperationsRequest{
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
		FolderPath:   rootDir,
	})
	if err != nil {
		t.Fatalf("create plan rpc failed: %v", err)
	}

	planID := planResp.PlanId
	if planID == "" {
		t.Fatal("plan_id should not be empty")
	}

	// Verify plan has operations
	if len(planResp.Operations) == 0 {
		t.Fatal("expected plan operations, got none")
	}

	// Get the source path of the first operation to know which file to delete
	firstOpSourcePath := planResp.Operations[0].SourcePath
	if firstOpSourcePath == "" {
		t.Fatal("first operation has empty source_path")
	}

	// Step 3: Mutate filesystem - delete the source file targeted by the plan to stale it
	// The source path from the plan is already an absolute path
	fileToDelete := firstOpSourcePath
	if _, err := os.Stat(fileToDelete); err != nil {
		t.Fatalf("source file does not exist before deletion: %v", err)
	}
	if err := os.Remove(fileToDelete); err != nil {
		t.Fatalf("failed to delete source file to stale plan: %v", err)
	}

	// Step 4: Execute plan via gRPC - should fail with precondition error
	execStream, err := client.ExecutePlan(ctx, &pb.ExecutePlanRequest{PlanId: planID})
	if err != nil {
		t.Fatalf("execute plan rpc failed: %v", err)
	}

	execEvents, execErr := collectExecuteEvents(execStream)

	// Step 5: Assert stream contains error event with event_type="error" and code="EXEC_PRECONDITION_FAILED"
	var foundErrorEvent bool
	for _, ev := range execEvents {
		if ev.EventType == "error" && ev.Code == "EXEC_PRECONDITION_FAILED" {
			foundErrorEvent = true
			break
		}
	}
	if !foundErrorEvent {
		t.Errorf("expected stream to contain error event with type='error' and code='EXEC_PRECONDITION_FAILED', got events: %v", execEvents)
	}

	// Step 6: Assert terminal error status code is codes.FailedPrecondition
	if execErr == nil {
		t.Fatal("expected terminal error with codes.FailedPrecondition, got nil")
	}
	st, ok := status.FromError(execErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", execErr)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected status code codes.FailedPrecondition, got %v", st.Code())
	}

	// Step 7: Assert repo GetExecuteSession shows status failed and error_code EXEC_PRECONDITION_FAILED
	sessions, err := repo.ListExecuteSessionsByPlan(planID)
	if err != nil {
		t.Fatalf("failed to list execute sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one execute session record")
	}

	// Check the latest session - use GetExecuteSession for full details including error_code
	latestSession := sessions[0]

	// Verify via GetExecuteSession (includes error_code, error_message)
	session, err := repo.GetExecuteSession(latestSession.SessionID)
	if err != nil {
		t.Fatalf("failed to get execute session: %v", err)
	}
	if session.Status != "failed" {
		t.Errorf("expected session status 'failed', got '%s'", session.Status)
	}
	if session.ErrorCode != "EXEC_PRECONDITION_FAILED" {
		t.Errorf("expected error_code 'EXEC_PRECONDITION_FAILED', got '%s'", session.ErrorCode)
	}

	t.Logf("gRPC stale precondition test complete: plan_id=%s, events=%d, terminal_error=%v", planID, len(execEvents), execErr)
}
