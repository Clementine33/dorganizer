package grpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type delegationRecorder struct {
	called  bool
	lastReq planusecase.Request
	resp    planusecase.Response
	err     error
}

func (r *delegationRecorder) Plan(_ context.Context, req planusecase.Request) (planusecase.Response, error) {
	r.called = true
	r.lastReq = req
	return r.resp, r.err
}

func TestPlanOperations_DelegatesToUsecase(t *testing.T) {
	recorder := &delegationRecorder{
		resp: planusecase.Response{
			PlanID: "delegated-plan",
			Operations: []planusecase.Operation{
				{Type: "delete", SourcePath: "test.mp3", TargetPath: "test-deleted.mp3"},
				{Type: "convert_and_delete", SourcePath: "test.wav", TargetPath: "test.mp3"},
			},
			Errors: []planusecase.FolderError{
				{Code: "TEST_ERROR", Message: "test error", FolderPath: "/test"},
			},
			SuccessfulFolders: []string{"/success"},
			RootPath:          "/root",
			Summary: planusecase.Summary{
				OperationCount:  2,
				ErrorCount:      1,
				TotalCount:      3,
				ActionableCount: 2,
				SummaryReason:   "ACTIONABLE",
			},
			SnapshotToken: "snap-1",
		},
	}

	server := NewOnseiServerWithServices(recorder, nil, nil, "", "")

	req := &pb.PlanOperationsRequest{
		PlanType:             "single_delete",
		SourceFiles:          []string{"test.mp3"},
		TargetFormat:         "prune:both",
		FolderPath:           "/folder",
		FolderPaths:          []string{"/folder1", "/folder2"},
		PruneMatchedExcluded: true,
	}
	resp, err := server.PlanOperations(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !recorder.called {
		t.Fatal("expected planService.Plan to be called, but it was not")
	}
	if recorder.lastReq.PlanType != "single_delete" {
		t.Errorf("PlanType = %q, want %q", recorder.lastReq.PlanType, "single_delete")
	}
	if len(recorder.lastReq.SourceFiles) != 1 || recorder.lastReq.SourceFiles[0] != "test.mp3" {
		t.Errorf("SourceFiles = %v, want [test.mp3]", recorder.lastReq.SourceFiles)
	}
	if recorder.lastReq.TargetFormat != "prune:both" {
		t.Errorf("TargetFormat = %q, want %q", recorder.lastReq.TargetFormat, "prune:both")
	}
	if recorder.lastReq.FolderPath != "/folder" {
		t.Errorf("FolderPath = %q, want %q", recorder.lastReq.FolderPath, "/folder")
	}
	if len(recorder.lastReq.FolderPaths) != 2 {
		t.Errorf("FolderPaths len = %d, want 2", len(recorder.lastReq.FolderPaths))
	}
	if !recorder.lastReq.PruneMatchedExcluded {
		t.Error("PruneMatchedExcluded should be true")
	}

	// Verify response mapping: top-level fields
	if resp.GetPlanId() != "delegated-plan" {
		t.Errorf("PlanId = %q, want %q", resp.GetPlanId(), "delegated-plan")
	}
	if resp.GetActionableCount() != 2 {
		t.Errorf("ActionableCount = %d, want 2", resp.GetActionableCount())
	}
	if resp.GetTotalCount() != 3 {
		t.Errorf("TotalCount = %d, want 3", resp.GetTotalCount())
	}
	if resp.GetSummaryReason() != "ACTIONABLE" {
		t.Errorf("SummaryReason = %q, want %q", resp.GetSummaryReason(), "ACTIONABLE")
	}

	// Verify response mapping: operations (including operation_type and target_path)
	if len(resp.GetOperations()) != 2 {
		t.Fatalf("Operations len = %d, want 2", len(resp.GetOperations()))
	}
	op0 := resp.GetOperations()[0]
	if op0.GetSourcePath() != "test.mp3" {
		t.Errorf("Op[0] SourcePath = %q, want %q", op0.GetSourcePath(), "test.mp3")
	}
	if op0.GetOperationType() != "delete" {
		t.Errorf("Op[0] OperationType = %q, want %q", op0.GetOperationType(), "delete")
	}
	if op0.GetTargetPath() != "test-deleted.mp3" {
		t.Errorf("Op[0] TargetPath = %q, want %q", op0.GetTargetPath(), "test-deleted.mp3")
	}
	op1 := resp.GetOperations()[1]
	if op1.GetSourcePath() != "test.wav" {
		t.Errorf("Op[1] SourcePath = %q, want %q", op1.GetSourcePath(), "test.wav")
	}
	if op1.GetOperationType() != "convert_and_delete" {
		t.Errorf("Op[1] OperationType = %q, want %q", op1.GetOperationType(), "convert_and_delete")
	}
	if op1.GetTargetPath() != "test.mp3" {
		t.Errorf("Op[1] TargetPath = %q, want %q", op1.GetTargetPath(), "test.mp3")
	}

	// Verify response mapping: folder errors (adapter-owned fields)
	if len(resp.GetPlanErrors()) != 1 {
		t.Fatalf("PlanErrors len = %d, want 1", len(resp.GetPlanErrors()))
	}
	pe := resp.GetPlanErrors()[0]
	if pe.GetStage() != "plan" {
		t.Errorf("FolderError.Stage = %q, want %q (adapter hard-codes \"plan\")", pe.GetStage(), "plan")
	}
	if pe.GetCode() != "TEST_ERROR" {
		t.Errorf("FolderError.Code = %q, want %q", pe.GetCode(), "TEST_ERROR")
	}
	if pe.GetMessage() != "test error" {
		t.Errorf("FolderError.Message = %q, want %q", pe.GetMessage(), "test error")
	}
	if pe.GetFolderPath() != "/test" {
		t.Errorf("FolderError.FolderPath = %q, want %q", pe.GetFolderPath(), "/test")
	}
	if pe.GetPlanId() != "delegated-plan" {
		t.Errorf("FolderError.PlanId = %q, want %q (from usecase PlanID)", pe.GetPlanId(), "delegated-plan")
	}
	if pe.GetRootPath() != "/root" {
		t.Errorf("FolderError.RootPath = %q, want %q (from usecase RootPath)", pe.GetRootPath(), "/root")
	}
	if pe.GetTimestamp() == "" {
		t.Error("FolderError.Timestamp must not be empty (adapter generates RFC3339Nano)")
	}
	if pe.GetEventId() == "" {
		t.Error("FolderError.EventId must not be empty (adapter generates evt-*)")
	}
	if !strings.HasPrefix(pe.GetEventId(), "evt-") {
		t.Errorf("FolderError.EventId = %q, want evt-* prefix", pe.GetEventId())
	}

	// Verify successful_folders
	if len(resp.GetSuccessfulFolders()) != 1 {
		t.Errorf("SuccessfulFolders len = %d, want 1", len(resp.GetSuccessfulFolders()))
	}
	if resp.GetSuccessfulFolders()[0] != "/success" {
		t.Errorf("SuccessfulFolders[0] = %q, want %q", resp.GetSuccessfulFolders()[0], "/success")
	}
}

// TestPlanOperations_DelegatesErrorToUsecase verifies the adapter:
//  1. Propagates errors from the usecase as gRPC errors.
//  2. Maps plain errors to codes.Internal.
//  3. Maps plan.Error kinds (invalid_argument, already_exists, internal) to
//     the correct gRPC status codes.
func TestPlanOperations_DelegatesErrorToUsecase(t *testing.T) {
	tests := []struct {
		name       string
		usecaseErr error
		wantCode   codes.Code
		wantMsg    string
	}{
		{
			name:       "plain error maps to Internal",
			usecaseErr: errors.New("plan failed: invalid argument"),
			wantCode:   codes.Internal,
			wantMsg:    "plan failed: invalid argument",
		},
		{
			name: "invalid_argument kind maps to InvalidArgument",
			usecaseErr: planusecase.NewError(
				planusecase.ErrKindInvalidArgument,
				"CONFIG_UNAVAILABLE",
				"prune regex config unavailable: read error",
				nil,
			),
			wantCode: codes.InvalidArgument,
			wantMsg:  "prune regex config unavailable: read error",
		},
		{
			name: "already_exists kind maps to AlreadyExists",
			usecaseErr: planusecase.NewError(
				planusecase.ErrKindAlreadyExists,
				"PLAN_ID_CONFLICT",
				"plan some-plan already exists",
				nil,
			),
			wantCode: codes.AlreadyExists,
			wantMsg:  "plan some-plan already exists",
		},
		{
			name: "internal kind maps to Internal",
			usecaseErr: planusecase.NewError(
				planusecase.ErrKindInternal,
				"ANALYZE_FAILED",
				"analyze: db error",
				nil,
			),
			wantCode: codes.Internal,
			wantMsg:  "analyze: db error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &delegationRecorder{err: tt.usecaseErr}
			server := NewOnseiServerWithServices(recorder, nil, nil, "", "")

			req := &pb.PlanOperationsRequest{
				PlanType:    "slim",
				SourceFiles: []string{"test.mp3"},
			}
			_, err := server.PlanOperations(context.Background(), req)

			if err == nil {
				t.Fatal("expected error from usecase delegation, got nil")
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected gRPC status error, got %T: %v", err, err)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("status code = %v, want %v", st.Code(), tt.wantCode)
			}
			if st.Message() != tt.wantMsg {
				t.Errorf("status message = %q, want %q", st.Message(), tt.wantMsg)
			}
		})
	}
}

// TestPlanOperations_AdapterIsSelfContained proves that PlanOperations generates
// its own transport-layer IDs (event IDs) without relying on old grpc helpers.
func TestPlanOperations_AdapterIsSelfContained(t *testing.T) {
	recorder := &delegationRecorder{
		resp: planusecase.Response{
			PlanID: "plan-self-contained",
			Errors: []planusecase.FolderError{
				{Code: "E1", Message: "err1", FolderPath: "/a"},
				{Code: "E2", Message: "err2", FolderPath: "/b"},
			},
		},
	}

	server := NewOnseiServerWithServices(recorder, nil, nil, "", "")
	req := &pb.PlanOperationsRequest{
		PlanType:    "single_delete",
		SourceFiles: []string{"test.mp3"},
	}
	resp, err := server.PlanOperations(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PlanId comes from the usecase, not generated by the adapter.
	if resp.GetPlanId() != "plan-self-contained" {
		t.Errorf("PlanId = %q, want plan-self-contained", resp.GetPlanId())
	}

	// EventId must be generated by the adapter's own newEventID() — each
	// FolderError gets a unique evt-* identifier.
	seen := make(map[string]bool)
	for _, pe := range resp.GetPlanErrors() {
		eid := pe.GetEventId()
		if eid == "" {
			t.Error("EventId must not be empty")
			continue
		}
		if !strings.HasPrefix(eid, "evt-") {
			t.Errorf("EventId must have evt- prefix, got %q", eid)
		}
		if seen[eid] {
			t.Errorf("duplicate EventId: %s", eid)
		}
		seen[eid] = true
	}
	if len(seen) != 2 {
		t.Errorf("expected 2 unique event IDs, got %d", len(seen))
	}
}
