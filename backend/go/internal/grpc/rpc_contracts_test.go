package grpc

import (
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

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

// assertFieldAbsent verifies a field is absent from a message descriptor.
func assertFieldAbsent(t *testing.T, md protoreflect.MessageDescriptor, fieldName string) {
	t.Helper()
	fd := md.Fields().ByName(protoreflect.Name(fieldName))
	if fd != nil {
		t.Fatalf("field %q should be absent in message %q", fieldName, md.FullName())
	}
}

// =============================================================================
// JobEvent wire-contract
// =============================================================================

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

// =============================================================================
// PlanOperationsResponse wire-contract
// =============================================================================

// TestContracts_PlanOperationsResponseFieldNumbers asserts exact protobuf field
// numbers for PlanOperationsResponse. These must never change.
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

// =============================================================================
// FolderError wire-contract
// =============================================================================

// TestContracts_FolderErrorFieldNumbers asserts exact protobuf field numbers
// for FolderError. These must never change.
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

// =============================================================================
// RefreshFoldersRequest wire-contract
// =============================================================================

// TestContracts_RefreshFoldersRequestFieldNumbers asserts exact protobuf field
// numbers for RefreshFoldersRequest. These must never change.
func TestContracts_RefreshFoldersRequestFieldNumbers(t *testing.T) {
	md := (&pb.RefreshFoldersRequest{}).ProtoReflect().Descriptor()

	assertFieldNumber(t, md, "root_path", 1)
	assertFieldNumber(t, md, "folder_paths", 2)
}

// =============================================================================
// RefreshFoldersResponse wire-contract
// =============================================================================

// TestContracts_RefreshFoldersResponseFieldNumbers asserts exact protobuf field
// numbers for RefreshFoldersResponse. These must never change.
func TestContracts_RefreshFoldersResponseFieldNumbers(t *testing.T) {
	md := (&pb.RefreshFoldersResponse{}).ProtoReflect().Descriptor()

	assertFieldNumber(t, md, "successful_folders", 1)
	assertFieldNumber(t, md, "errors", 2)
}
