package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRefreshFolders_ValidationErrors tests request-level validation.
func TestRefreshFolders_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		req         *pb.RefreshFoldersRequest
		wantCode    codes.Code
		wantMessage string
	}{
		{
			name:        "empty root_path returns error",
			req:         &pb.RefreshFoldersRequest{RootPath: "", FolderPaths: []string{"/folder"}},
			wantCode:    codes.InvalidArgument,
			wantMessage: "root_path is required",
		},
		{
			name:        "empty folder_paths returns error",
			req:         &pb.RefreshFoldersRequest{RootPath: "/root", FolderPaths: []string{}},
			wantCode:    codes.InvalidArgument,
			wantMessage: "folder_paths is required",
		},
		{
			name:        "nil folder_paths returns error",
			req:         &pb.RefreshFoldersRequest{RootPath: "/root", FolderPaths: nil},
			wantCode:    codes.InvalidArgument,
			wantMessage: "folder_paths is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "onsei-test-refresh-validation-*")
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
			_, err = server.RefreshFolders(context.Background(), tt.req)

			if err == nil {
				t.Fatal("expected error but got nil")
			}

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got %T: %v", err, err)
			}

			if st.Code() != tt.wantCode {
				t.Errorf("expected code %v, got %v", tt.wantCode, st.Code())
			}

			if tt.wantMessage != "" && st.Message() != tt.wantMessage {
				t.Errorf("expected message %q, got %q", tt.wantMessage, st.Message())
			}
		})
	}
}

// TestRefreshFolders_StructuredPerFolderResult tests that the RPC returns
// structured success/failure per folder, not failing the whole RPC for folder errors.
func TestRefreshFolders_StructuredPerFolderResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-refresh-structured-*")
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

	// Create a valid folder structure
	validFolder := filepath.Join(tmpDir, "valid_folder")
	if err := os.MkdirAll(validFolder, 0755); err != nil {
		t.Fatalf("failed to create valid folder: %v", err)
	}

	// Create audio file in valid folder
	audioFile := filepath.Join(validFolder, "test.flac")
	if err := os.WriteFile(audioFile, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create audio file: %v", err)
	}

	// Create an invalid folder path (doesn't exist)
	invalidFolder := filepath.Join(tmpDir, "nonexistent_folder")

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Request refresh of both folders
	req := &pb.RefreshFoldersRequest{
		RootPath:    tmpDir,
		FolderPaths: []string{validFolder, invalidFolder},
	}

	resp, err := server.RefreshFolders(context.Background(), req)
	if err != nil {
		t.Fatalf("RefreshFolders should not return RPC error for folder-level failures: %v", err)
	}

	// Verify structured response
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify successful_folders contains the valid folder
	successFolders := resp.GetSuccessfulFolders()
	validFolderNorm := filepath.ToSlash(filepath.Clean(validFolder))
	foundValid := false
	for _, sf := range successFolders {
		if sf == validFolderNorm {
			foundValid = true
			break
		}
	}
	if !foundValid {
		t.Errorf("expected successful_folders to contain %q, got %v", validFolderNorm, successFolders)
	}

	// Verify errors contains the invalid folder
	folderErrors := resp.GetErrors()
	invalidFolderNorm := filepath.ToSlash(filepath.Clean(invalidFolder))
	foundInvalid := false
	for _, fe := range folderErrors {
		if fe.GetFolderPath() == invalidFolderNorm {
			foundInvalid = true
			// Verify structured error fields
			if fe.GetStage() != "refresh" {
				t.Errorf("expected stage 'refresh', got %q", fe.GetStage())
			}
			if fe.GetCode() == "" {
				t.Error("expected non-empty error code")
			}
			if fe.GetMessage() == "" {
				t.Error("expected non-empty error message")
			}
			if fe.GetRootPath() == "" {
				t.Error("expected non-empty root_path")
			}
			if fe.GetTimestamp() == "" {
				t.Error("expected non-empty timestamp")
			}
			if fe.GetEventId() == "" {
				t.Error("expected non-empty event_id")
			}
			break
		}
	}
	if !foundInvalid {
		t.Errorf("expected errors to contain entry for %q, got %v", invalidFolderNorm, folderErrors)
	}
}

// TestRefreshFolders_FolderScopedSemantics tests that the handler calls ScanFolder
// (folder-scoped refresh), not ScanRoot (root-wide scan).
func TestRefreshFolders_FolderScopedSemantics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-refresh-scoped-*")
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

	// Create folder structure with subfolder
	folder1 := filepath.Join(tmpDir, "folder1")
	folder2 := filepath.Join(tmpDir, "folder2")
	if err := os.MkdirAll(folder1, 0755); err != nil {
		t.Fatalf("failed to create folder1: %v", err)
	}
	if err := os.MkdirAll(folder2, 0755); err != nil {
		t.Fatalf("failed to create folder2: %v", err)
	}

	// Create audio files
	audio1 := filepath.Join(folder1, "track1.flac")
	audio2 := filepath.Join(folder2, "track2.flac")
	if err := os.WriteFile(audio1, []byte("dummy flac 1"), 0644); err != nil {
		t.Fatalf("failed to create audio1: %v", err)
	}
	if err := os.WriteFile(audio2, []byte("dummy flac 2"), 0644); err != nil {
		t.Fatalf("failed to create audio2: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Request refresh of specific folders
	req := &pb.RefreshFoldersRequest{
		RootPath:    tmpDir,
		FolderPaths: []string{folder1, folder2},
	}

	resp, err := server.RefreshFolders(context.Background(), req)
	if err != nil {
		t.Fatalf("RefreshFolders failed: %v", err)
	}

	// Verify both folders are in successful_folders (folder-scoped scan)
	successFolders := resp.GetSuccessfulFolders()
	folder1Norm := filepath.ToSlash(filepath.Clean(folder1))
	folder2Norm := filepath.ToSlash(filepath.Clean(folder2))

	found1 := false
	found2 := false
	for _, sf := range successFolders {
		if sf == folder1Norm {
			found1 = true
		}
		if sf == folder2Norm {
			found2 = true
		}
	}

	if !found1 {
		t.Errorf("expected successful_folders to contain %q", folder1Norm)
	}
	if !found2 {
		t.Errorf("expected successful_folders to contain %q", folder2Norm)
	}

	// Verify entries are in database for both folders (proves folder-scoped scan)
	var count1, count2 int
	err = repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries WHERE path LIKE ?",
		filepath.ToSlash(folder1)+"/%",
	).Scan(&count1)
	if err != nil {
		t.Fatalf("failed to count entries in folder1: %v", err)
	}

	err = repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries WHERE path LIKE ?",
		filepath.ToSlash(folder2)+"/%",
	).Scan(&count2)
	if err != nil {
		t.Fatalf("failed to count entries in folder2: %v", err)
	}

	if count1 == 0 {
		t.Error("expected entries in folder1 after folder-scoped refresh")
	}
	if count2 == 0 {
		t.Error("expected entries in folder2 after folder-scoped refresh")
	}

	// Verify no entries from outside the specified folders
	var totalEntries int
	err = repo.DB().QueryRow("SELECT COUNT(*) FROM entries").Scan(&totalEntries)
	if err != nil {
		t.Fatalf("failed to count total entries: %v", err)
	}
	if totalEntries != count1+count2 {
		t.Errorf("expected only entries from specified folders, got total=%d, folder1=%d, folder2=%d",
			totalEntries, count1, count2)
	}
}

// TestRefreshFolders_AllFoldersSucceed tests that when all folders succeed,
// successful_folders contains all folders and errors is empty.
func TestRefreshFolders_AllFoldersSucceed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-refresh-all-success-*")
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

	// Create multiple valid folders
	folderA := filepath.Join(tmpDir, "folderA")
	folderB := filepath.Join(tmpDir, "folderB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatalf("failed to create folderA: %v", err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatalf("failed to create folderB: %v", err)
	}

	// Create audio files
	audioA := filepath.Join(folderA, "track.flac")
	audioB := filepath.Join(folderB, "track.flac")
	if err := os.WriteFile(audioA, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create audioA: %v", err)
	}
	if err := os.WriteFile(audioB, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create audioB: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	req := &pb.RefreshFoldersRequest{
		RootPath:    tmpDir,
		FolderPaths: []string{folderA, folderB},
	}

	resp, err := server.RefreshFolders(context.Background(), req)
	if err != nil {
		t.Fatalf("RefreshFolders failed: %v", err)
	}

	// Verify all folders in successful_folders
	if len(resp.GetSuccessfulFolders()) != 2 {
		t.Errorf("expected 2 successful folders, got %d: %v", len(resp.GetSuccessfulFolders()), resp.GetSuccessfulFolders())
	}

	// Verify no errors
	if len(resp.GetErrors()) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(resp.GetErrors()), resp.GetErrors())
	}
}

// TestRefreshFolders_AllFoldersFail tests that when all folders fail,
// the RPC still succeeds with empty successful_folders and errors for each folder.
func TestRefreshFolders_AllFoldersFail(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-refresh-all-fail-*")
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

	// Use non-existent folders
	folderX := filepath.Join(tmpDir, "nonexistentX")
	folderY := filepath.Join(tmpDir, "nonexistentY")

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	req := &pb.RefreshFoldersRequest{
		RootPath:    tmpDir,
		FolderPaths: []string{folderX, folderY},
	}

	// RPC should NOT return error (per-folder errors collected in response)
	resp, err := server.RefreshFolders(context.Background(), req)
	if err != nil {
		t.Fatalf("RefreshFolders should not return RPC error for folder-level failures: %v", err)
	}

	// Verify empty successful_folders
	if len(resp.GetSuccessfulFolders()) != 0 {
		t.Errorf("expected 0 successful folders, got %d: %v", len(resp.GetSuccessfulFolders()), resp.GetSuccessfulFolders())
	}

	// Verify errors for each folder
	if len(resp.GetErrors()) != 2 {
		t.Errorf("expected 2 errors, got %d", len(resp.GetErrors()))
	}
}

// TestRefreshFolders_UsesScanFolderNotScanRoot verifies that RefreshFolders
// calls ScanFolder (folder-scoped) instead of ScanRoot (root-wide).
// This is a semantic test that validates the handler behavior.
func TestRefreshFolders_UsesScanFolderNotScanRoot(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-refresh-scanfolder-*")
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

	// Create nested structure
	// root/
	//   folderA/
	//     track.flac
	//   folderB/
	//     track.flac
	//   folderC/
	//     track.flac  <- NOT in request, should NOT be scanned

	folderA := filepath.Join(tmpDir, "folderA")
	folderB := filepath.Join(tmpDir, "folderB")
	folderC := filepath.Join(tmpDir, "folderC")

	for _, f := range []string{folderA, folderB, folderC} {
		if err := os.MkdirAll(f, 0755); err != nil {
			t.Fatalf("failed to create folder: %v", err)
		}
		audio := filepath.Join(f, "track.flac")
		if err := os.WriteFile(audio, []byte("dummy flac"), 0644); err != nil {
			t.Fatalf("failed to create audio file: %v", err)
		}
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Request refresh of ONLY folderA and folderB
	req := &pb.RefreshFoldersRequest{
		RootPath:    tmpDir,
		FolderPaths: []string{folderA, folderB},
	}

	resp, err := server.RefreshFolders(context.Background(), req)
	if err != nil {
		t.Fatalf("RefreshFolders failed: %v", err)
	}

	// Verify folderA and folderB are in successful_folders
	successFolders := resp.GetSuccessfulFolders()
	folderANorm := filepath.ToSlash(filepath.Clean(folderA))
	folderBNorm := filepath.ToSlash(filepath.Clean(folderB))

	foundA := false
	foundB := false
	for _, sf := range successFolders {
		if sf == folderANorm {
			foundA = true
		}
		if sf == folderBNorm {
			foundB = true
		}
	}

	if !foundA || !foundB {
		t.Errorf("expected folderA and folderB in successful_folders, got %v", successFolders)
	}

	// Verify entries exist for folderA and folderB
	var countA, countB, countC int
	repo.DB().QueryRow("SELECT COUNT(*) FROM entries WHERE path LIKE ?", folderANorm+"/%").Scan(&countA)
	repo.DB().QueryRow("SELECT COUNT(*) FROM entries WHERE path LIKE ?", folderBNorm+"/%").Scan(&countB)
	repo.DB().QueryRow("SELECT COUNT(*) FROM entries WHERE path LIKE ?", filepath.ToSlash(folderC)+"/%").Scan(&countC)

	if countA == 0 {
		t.Error("expected entries in folderA after folder-scoped refresh")
	}
	if countB == 0 {
		t.Error("expected entries in folderB after folder-scoped refresh")
	}

	// CRITICAL: folderC should NOT have entries (proves ScanFolder, not ScanRoot)
	if countC != 0 {
		t.Errorf("folderC should NOT have entries (ScanFolder was used, not ScanRoot), got %d entries", countC)
	}
}
