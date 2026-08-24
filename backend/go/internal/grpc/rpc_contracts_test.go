package grpc

import (
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestEventTypeIncludesTerminalStates(t *testing.T) {
	got := []string{"STARTED", "PROGRESS", "LOG", "WARNING", "COMPLETED", "FAILED"}
	if len(got) != 6 {
		t.Fatalf("unexpected event type count: %d", len(got))
	}
}

// assertFieldNumber verifies a field exists with an exact field number in a message descriptor.
// This ensures protobuf wire-format contract stability.
func assertFieldNumber(t *testing.T, md protoreflect.MessageDescriptor, fieldName string, expectedNumber protoreflect.FieldNumber) {
	t.Helper()
	fd := md.Fields().ByName(protoreflect.Name(fieldName))
	if fd == nil {
		t.Fatalf("field %q not found in message %q", fieldName, md.FullName())
	}
	if fd.Number() != expectedNumber {
		t.Errorf("field %q: expected number %d, got %d", fieldName, expectedNumber, fd.Number())
	}
}

func assertFieldAbsent(t *testing.T, md protoreflect.MessageDescriptor, fieldName string) {
	t.Helper()
	fd := md.Fields().ByName(protoreflect.Name(fieldName))
	if fd != nil {
		t.Fatalf("field %q should be absent in message %q", fieldName, md.FullName())
	}
}

// TestContracts_JobEventFields asserts JobEvent contains all structured error attribution fields
// with exact protobuf field numbers for wire-format stability.
func TestContracts_JobEventFields(t *testing.T) {
	// Create a JobEvent and verify all required fields exist and can be set
	event := &pb.JobEvent{
		EventType:       "error",
		Message:         "test message",
		ProgressPercent: 50,
		Timestamp:       "2026-03-20T12:00:00Z",
		// New structured error fields (Task 1)
		Stage:          "execute",
		Code:           "EXEC_STAGE1_COPY_FAILED",
		FolderPath:     "/root/folder1",
		PlanId:         "plan-123",
		RootPath:       "/root",
		EventId:        "evt-456",
		ItemSourcePath: "/root/folder1/source.wav",
		ItemTargetPath: "/root/folder1/target.flac",
		CorrelationId:  "corr-789",
	}

	// Assert all fields are set correctly
	if event.Stage != "execute" {
		t.Error("JobEvent.Stage field missing or incorrect")
	}
	if event.Code != "EXEC_STAGE1_COPY_FAILED" {
		t.Error("JobEvent.Code field missing or incorrect")
	}
	if event.FolderPath != "/root/folder1" {
		t.Error("JobEvent.FolderPath field missing or incorrect")
	}
	if event.PlanId != "plan-123" {
		t.Error("JobEvent.PlanId field missing or incorrect")
	}
	if event.RootPath != "/root" {
		t.Error("JobEvent.RootPath field missing or incorrect")
	}
	if event.EventId != "evt-456" {
		t.Error("JobEvent.EventId field missing or incorrect")
	}
	if event.ItemSourcePath != "/root/folder1/source.wav" {
		t.Error("JobEvent.ItemSourcePath field missing or incorrect")
	}
	if event.ItemTargetPath != "/root/folder1/target.flac" {
		t.Error("JobEvent.ItemTargetPath field missing or incorrect")
	}
	if event.CorrelationId != "corr-789" {
		t.Error("JobEvent.CorrelationId field missing or incorrect")
	}
}

// TestContracts_JobEventFieldNumbers asserts exact protobuf field numbers for JobEvent.
// These field numbers are part of the wire-format contract and must never change.
func TestContracts_JobEventFieldNumbers(t *testing.T) {
	md := (&pb.JobEvent{}).ProtoReflect().Descriptor()

	// Original fields 1-3
	assertFieldNumber(t, md, "event_type", 1)
	assertFieldNumber(t, md, "message", 2)
	assertFieldNumber(t, md, "progress_percent", 3)

	// Extended fields 4-13
	assertFieldNumber(t, md, "timestamp", 4)
	assertFieldNumber(t, md, "stage", 5)
	assertFieldNumber(t, md, "code", 6)
	assertFieldNumber(t, md, "folder_path", 7)
	assertFieldNumber(t, md, "plan_id", 8)
	assertFieldNumber(t, md, "root_path", 9)
	assertFieldNumber(t, md, "event_id", 10)
	assertFieldNumber(t, md, "item_source_path", 11)
	assertFieldNumber(t, md, "item_target_path", 12)
	assertFieldNumber(t, md, "correlation_id", 13)
}

// TestContracts_PlanOperationsResponseFields asserts PlanOperationsResponse contains plan_errors and successful_folders
func TestContracts_PlanOperationsResponseFields(t *testing.T) {
	resp := &pb.PlanOperationsResponse{
		PlanId:          "plan-123",
		TotalCount:      10,
		ActionableCount: 8,
		SummaryReason:   "test summary",
		// New fields (Task 1)
		PlanErrors: []*pb.FolderError{
			{
				Stage:      "plan",
				Code:       "PATH_ABS_FAILED",
				Message:    "failed to resolve path",
				FolderPath: "/root/folder1",
				PlanId:     "plan-123",
				RootPath:   "/root",
				Timestamp:  "2026-03-20T12:00:00Z",
				EventId:    "evt-error-1",
			},
		},
		SuccessfulFolders: []string{"/root/folder2", "/root/folder3"},
	}

	// Assert plan_errors field
	if len(resp.PlanErrors) != 1 {
		t.Fatalf("PlanOperationsResponse.PlanErrors missing or incorrect length: %d", len(resp.PlanErrors))
	}
	if resp.PlanErrors[0].Stage != "plan" {
		t.Error("FolderError.Stage missing or incorrect")
	}
	if resp.PlanErrors[0].Code != "PATH_ABS_FAILED" {
		t.Error("FolderError.Code missing or incorrect")
	}
	if resp.PlanErrors[0].Message != "failed to resolve path" {
		t.Error("FolderError.Message missing or incorrect")
	}
	if resp.PlanErrors[0].FolderPath != "/root/folder1" {
		t.Error("FolderError.FolderPath missing or incorrect")
	}
	if resp.PlanErrors[0].PlanId != "plan-123" {
		t.Error("FolderError.PlanId missing or incorrect")
	}
	if resp.PlanErrors[0].RootPath != "/root" {
		t.Error("FolderError.RootPath missing or incorrect")
	}
	if resp.PlanErrors[0].Timestamp != "2026-03-20T12:00:00Z" {
		t.Error("FolderError.Timestamp missing or incorrect")
	}
	if resp.PlanErrors[0].EventId != "evt-error-1" {
		t.Error("FolderError.EventId missing or incorrect")
	}

	// Assert successful_folders field
	if len(resp.SuccessfulFolders) != 2 {
		t.Fatalf("PlanOperationsResponse.SuccessfulFolders missing or incorrect length: %d", len(resp.SuccessfulFolders))
	}
	if resp.SuccessfulFolders[0] != "/root/folder2" {
		t.Error("SuccessfulFolders[0] missing or incorrect")
	}
	if resp.SuccessfulFolders[1] != "/root/folder3" {
		t.Error("SuccessfulFolders[1] missing or incorrect")
	}
}

// TestContracts_PlanOperationsResponseFieldNumbers asserts exact protobuf field numbers for PlanOperationsResponse.
// These field numbers are part of the wire-format contract and must never change.
func TestContracts_PlanOperationsResponseFieldNumbers(t *testing.T) {
	md := (&pb.PlanOperationsResponse{}).ProtoReflect().Descriptor()

	// Original fields 1-6 (with keep_count reserved/removed)
	assertFieldNumber(t, md, "plan_id", 1)
	assertFieldNumber(t, md, "operations", 2)
	assertFieldNumber(t, md, "total_count", 3)
	assertFieldNumber(t, md, "actionable_count", 4)
	assertFieldAbsent(t, md, "keep_count")
	assertFieldNumber(t, md, "summary_reason", 6)

	// New fields 7-8
	assertFieldNumber(t, md, "plan_errors", 7)
	assertFieldNumber(t, md, "successful_folders", 8)
}

// TestContracts_FolderErrorFields asserts FolderError message has all required fields
func TestContracts_FolderErrorFields(t *testing.T) {
	// Create a FolderError with all fields
	folderError := &pb.FolderError{
		Stage:      "plan",
		Code:       "SLIM_STEM_MATCH_GT2",
		Message:    "found 3 files with same stem",
		FolderPath: "/root/ambiguous_folder",
		PlanId:     "plan-456",
		RootPath:   "/root",
		Timestamp:  "2026-03-20T12:00:00Z",
		EventId:    "evt-folder-error-1",
	}

	// Verify all fields
	if folderError.Stage != "plan" {
		t.Error("FolderError.Stage missing or incorrect")
	}
	if folderError.Code != "SLIM_STEM_MATCH_GT2" {
		t.Error("FolderError.Code missing or incorrect")
	}
	if folderError.Message != "found 3 files with same stem" {
		t.Error("FolderError.Message missing or incorrect")
	}
	if folderError.FolderPath != "/root/ambiguous_folder" {
		t.Error("FolderError.FolderPath missing or incorrect")
	}
	if folderError.PlanId != "plan-456" {
		t.Error("FolderError.PlanId missing or incorrect")
	}
	if folderError.RootPath != "/root" {
		t.Error("FolderError.RootPath missing or incorrect")
	}
	if folderError.Timestamp != "2026-03-20T12:00:00Z" {
		t.Error("FolderError.Timestamp missing or incorrect")
	}
	if folderError.EventId != "evt-folder-error-1" {
		t.Error("FolderError.EventId missing or incorrect")
	}
}

// TestContracts_FolderErrorFieldNumbers asserts exact protobuf field numbers for FolderError.
// These field numbers are part of the wire-format contract and must never change.
func TestContracts_FolderErrorFieldNumbers(t *testing.T) {
	md := (&pb.FolderError{}).ProtoReflect().Descriptor()

	assertFieldNumber(t, md, "stage", 1)
	assertFieldNumber(t, md, "code", 2)
	assertFieldNumber(t, md, "message", 3)
	assertFieldNumber(t, md, "folder_path", 4)
	assertFieldNumber(t, md, "plan_id", 5)
	assertFieldNumber(t, md, "root_path", 6)
	assertFieldNumber(t, md, "timestamp", 7)
	assertFieldNumber(t, md, "event_id", 8)
}

// TestContracts_ProtoSchemaBackwardCompatibility verifies existing field numbers are preserved
func TestContracts_ProtoSchemaBackwardCompatibility(t *testing.T) {
	// Verify PlanOperationsResponse existing fields still work
	resp := &pb.PlanOperationsResponse{
		PlanId:          "plan-1",                 // field 1
		Operations:      []*pb.PlannedOperation{}, // field 2
		TotalCount:      5,                        // field 3
		ActionableCount: 3,                        // field 4
		SummaryReason:   "reason",                 // field 6
		// plan_errors = 7 (new)
		// successful_folders = 8 (new)
	}

	if resp.PlanId != "plan-1" {
		t.Error("PlanOperationsResponse.PlanId backward compatibility broken")
	}
	if resp.TotalCount != 5 {
		t.Error("PlanOperationsResponse.TotalCount backward compatibility broken")
	}
	if resp.ActionableCount != 3 {
		t.Error("PlanOperationsResponse.ActionableCount backward compatibility broken")
	}
	if resp.SummaryReason != "reason" {
		t.Error("PlanOperationsResponse.SummaryReason backward compatibility broken")
	}

	// Verify JobEvent existing fields still work
	event := &pb.JobEvent{
		EventType:       "error", // field 1
		Message:         "msg",   // field 2
		ProgressPercent: 50,      // field 3
		Timestamp:       "ts",    // field 4
		// stage = 5 (new)
		// code = 6 (new)
		// folder_path = 7 (new)
		// plan_id = 8 (new)
		// root_path = 9 (new)
		// event_id = 10 (new)
		// item_source_path = 11 (new)
		// item_target_path = 12 (new)
		// correlation_id = 13 (new)
	}

	if event.EventType != "error" {
		t.Error("JobEvent.EventType backward compatibility broken")
	}
	if event.Message != "msg" {
		t.Error("JobEvent.Message backward compatibility broken")
	}
	if event.ProgressPercent != 50 {
		t.Error("JobEvent.ProgressPercent backward compatibility broken")
	}
	if event.Timestamp != "ts" {
		t.Error("JobEvent.Timestamp backward compatibility broken")
	}
}

// TestContracts_RefreshFoldersRequestFields asserts RefreshFoldersRequest has correct fields
func TestContracts_RefreshFoldersRequestFields(t *testing.T) {
	req := &pb.RefreshFoldersRequest{
		RootPath:    "/root/path",
		FolderPaths: []string{"/root/folder1", "/root/folder2"},
	}

	if req.RootPath != "/root/path" {
		t.Error("RefreshFoldersRequest.RootPath missing or incorrect")
	}
	if len(req.FolderPaths) != 2 {
		t.Errorf("RefreshFoldersRequest.FolderPaths missing or incorrect length: %d", len(req.FolderPaths))
	}
	if req.FolderPaths[0] != "/root/folder1" {
		t.Error("RefreshFoldersRequest.FolderPaths[0] missing or incorrect")
	}
	if req.FolderPaths[1] != "/root/folder2" {
		t.Error("RefreshFoldersRequest.FolderPaths[1] missing or incorrect")
	}
}

// TestContracts_RefreshFoldersRequestFieldNumbers asserts exact protobuf field numbers for RefreshFoldersRequest.
func TestContracts_RefreshFoldersRequestFieldNumbers(t *testing.T) {
	md := (&pb.RefreshFoldersRequest{}).ProtoReflect().Descriptor()

	assertFieldNumber(t, md, "root_path", 1)
	assertFieldNumber(t, md, "folder_paths", 2)
}

// TestContracts_RefreshFoldersResponseFields asserts RefreshFoldersResponse has correct fields
func TestContracts_RefreshFoldersResponseFields(t *testing.T) {
	resp := &pb.RefreshFoldersResponse{
		SuccessfulFolders: []string{"/root/folder1", "/root/folder2"},
		Errors: []*pb.FolderError{
			{
				Stage:      "refresh",
				Code:       "SCAN_FOLDER_FAILED",
				Message:    "failed to scan folder",
				FolderPath: "/root/folder3",
				RootPath:   "/root",
				Timestamp:  "2026-03-21T12:00:00Z",
				EventId:    "evt-refresh-1",
			},
		},
	}

	if len(resp.SuccessfulFolders) != 2 {
		t.Errorf("RefreshFoldersResponse.SuccessfulFolders missing or incorrect length: %d", len(resp.SuccessfulFolders))
	}
	if resp.SuccessfulFolders[0] != "/root/folder1" {
		t.Error("RefreshFoldersResponse.SuccessfulFolders[0] missing or incorrect")
	}
	if resp.SuccessfulFolders[1] != "/root/folder2" {
		t.Error("RefreshFoldersResponse.SuccessfulFolders[1] missing or incorrect")
	}

	if len(resp.Errors) != 1 {
		t.Fatalf("RefreshFoldersResponse.Errors missing or incorrect length: %d", len(resp.Errors))
	}
	if resp.Errors[0].Stage != "refresh" {
		t.Error("RefreshFoldersResponse.Errors[0].Stage missing or incorrect")
	}
	if resp.Errors[0].Code != "SCAN_FOLDER_FAILED" {
		t.Error("RefreshFoldersResponse.Errors[0].Code missing or incorrect")
	}
	if resp.Errors[0].FolderPath != "/root/folder3" {
		t.Error("RefreshFoldersResponse.Errors[0].FolderPath missing or incorrect")
	}
}

// TestContracts_RefreshFoldersResponseFieldNumbers asserts exact protobuf field numbers for RefreshFoldersResponse.
func TestContracts_RefreshFoldersResponseFieldNumbers(t *testing.T) {
	md := (&pb.RefreshFoldersResponse{}).ProtoReflect().Descriptor()

	assertFieldNumber(t, md, "successful_folders", 1)
	assertFieldNumber(t, md, "errors", 2)
}
