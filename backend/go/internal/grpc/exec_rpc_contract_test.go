package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	executeusecase "github.com/onsei/organizer/backend/internal/usecase/execute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeExecuteService implements executeusecase.Service for adapter tests.
// It records the received request and returns canned events/result/error.
type fakeExecuteService struct {
	// lastReq captures the request passed by the adapter.
	lastReq executeusecase.Request

	result executeusecase.Result
	events []executeusecase.Event
	err    error
}

func (f *fakeExecuteService) Execute(_ context.Context, req executeusecase.Request, sink executeusecase.EventSink) (executeusecase.Result, error) {
	f.lastReq = req
	for _, evt := range f.events {
		if err := sink.Emit(evt); err != nil {
			return executeusecase.Result{}, err
		}
	}
	return f.result, f.err
}

// =============================================================================
// 1. Request mapping tests
// =============================================================================

// TestExecutePlan_RequestMapping verifies that the adapter correctly maps
// pb.ExecutePlanRequest fields (PlanId, SoftDelete) into executeusecase.Request.
func TestExecutePlan_RequestMapping(t *testing.T) {
	tests := []struct {
		name           string
		planID         string
		softDelete     bool
		wantPlanID     string
		wantSoftDelete bool
	}{
		{
			name:           "PlanId and SoftDelete=true",
			planID:         "plan-abc-123",
			softDelete:     true,
			wantPlanID:     "plan-abc-123",
			wantSoftDelete: true,
		},
		{
			name:           "PlanId and SoftDelete=false (default)",
			planID:         "plan-def-456",
			softDelete:     false,
			wantPlanID:     "plan-def-456",
			wantSoftDelete: false,
		},
		{
			name:           "empty PlanId passes through",
			planID:         "",
			softDelete:     true,
			wantPlanID:     "",
			wantSoftDelete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useCaseSvc := &fakeExecuteService{
				result: executeusecase.Result{PlanID: tt.wantPlanID, Status: "ok"},
			}
			server := &OnseiServer{executeService: useCaseSvc}
			stream := &mockServerStreamHelper{}

			req := &pb.ExecutePlanRequest{
				PlanId:     tt.planID,
				SoftDelete: tt.softDelete,
			}
			err := server.ExecutePlan(req, stream)
			if err != nil {
				t.Fatalf("ExecutePlan returned error: %v", err)
			}

			if useCaseSvc.lastReq.PlanID != tt.wantPlanID {
				t.Errorf("Request.PlanID = %q, want %q", useCaseSvc.lastReq.PlanID, tt.wantPlanID)
			}
			if useCaseSvc.lastReq.SoftDelete != tt.wantSoftDelete {
				t.Errorf("Request.SoftDelete = %v, want %v", useCaseSvc.lastReq.SoftDelete, tt.wantSoftDelete)
			}
		})
	}
}

// =============================================================================
// 2. Status mapping tests
// =============================================================================

// TestExecutePlan_StatusMapping verifies that the adapter correctly maps
// executeusecase.Error kinds to gRPC status codes via mapExecuteError.
func TestExecutePlan_StatusMapping(t *testing.T) {
	tests := []struct {
		name        string
		usecaseErr  error
		wantCode    codes.Code
		wantMessage string
	}{
		{
			name:        "plain error maps to Internal",
			usecaseErr:  errors.New("execute: db connection lost"),
			wantCode:    codes.Internal,
			wantMessage: "execute: db connection lost",
		},
		{
			name: "InvalidArgument maps to InvalidArgument",
			usecaseErr: executeusecase.NewError(
				executeusecase.ErrKindInvalidArgument,
				"INVALID_PLAN_ID",
				"plan_id is required",
				nil,
			),
			wantCode:    codes.InvalidArgument,
			wantMessage: "plan_id is required",
		},
		{
			name: "NotFound maps to NotFound",
			usecaseErr: executeusecase.NewError(
				executeusecase.ErrKindNotFound,
				"PLAN_NOT_FOUND",
				"Plan not found: plan-xyz",
				nil,
			),
			wantCode:    codes.NotFound,
			wantMessage: "Plan not found: plan-xyz",
		},
		{
			name: "FailedPrecondition maps to FailedPrecondition",
			usecaseErr: executeusecase.NewError(
				executeusecase.ErrKindFailedPrecondition,
				"EXECUTION_FAILED",
				"all folders failed",
				nil,
			),
			wantCode:    codes.FailedPrecondition,
			wantMessage: "all folders failed",
		},
		{
			name: "Internal kind maps to Internal",
			usecaseErr: executeusecase.NewError(
				executeusecase.ErrKindInternal,
				"CONFIG_INVALID",
				"load tools config: parse error",
				nil,
			),
			wantCode:    codes.Internal,
			wantMessage: "load tools config: parse error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useCaseSvc := &fakeExecuteService{err: tt.usecaseErr}
			server := &OnseiServer{executeService: useCaseSvc}
			stream := &mockServerStreamHelper{}

			req := &pb.ExecutePlanRequest{PlanId: "plan-1"}
			err := server.ExecutePlan(req, stream)

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
			if st.Message() != tt.wantMessage {
				t.Errorf("status message = %q, want %q", st.Message(), tt.wantMessage)
			}
		})
	}
}

// =============================================================================
// 3. Event protobuf mapping tests
// =============================================================================

// TestExecutePlan_EventMapping verifies that the grpcEventSink correctly maps
// all fields from executeusecase.Event to pb.JobEvent.
func TestExecutePlan_EventMapping(t *testing.T) {
	refTime := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

	// Fully populate a usecase.Event to verify all fields are mapped.
	useCaseEvt := executeusecase.Event{
		Type:            "started",
		Stage:           "execute",
		Code:            "OK",
		Message:         "Executing plan plan-xyz",
		PlanID:          "plan-xyz",
		RootPath:        "/data/Music",
		FolderPath:      "/data/Music/AlbumA",
		ItemSourcePath:  "/data/Music/AlbumA/song.wav",
		ItemTargetPath:  "/data/Music/AlbumA/song.mp3",
		ProgressPercent: 50,
		EventID:         "evt-abc-123",
		Timestamp:       refTime,
	}

	useCaseSvc := &fakeExecuteService{
		result: executeusecase.Result{PlanID: "plan-xyz", Status: "ok"},
		events: []executeusecase.Event{useCaseEvt},
	}
	server := &OnseiServer{executeService: useCaseSvc}
	stream := &mockServerStreamHelper{}

	req := &pb.ExecutePlanRequest{PlanId: "plan-xyz"}
	err := server.ExecutePlan(req, stream)
	if err != nil {
		t.Fatalf("ExecutePlan returned error: %v", err)
	}

	if len(stream.events) != 1 {
		t.Fatalf("expected 1 JobEvent, got %d", len(stream.events))
	}

	ev := stream.events[0]

	// Verify every field is mapped.
	if ev.GetEventType() != "started" {
		t.Errorf("EventType = %q, want %q", ev.GetEventType(), "started")
	}
	if ev.GetStage() != "execute" {
		t.Errorf("Stage = %q, want %q", ev.GetStage(), "execute")
	}
	if ev.GetCode() != "OK" {
		t.Errorf("Code = %q, want %q", ev.GetCode(), "OK")
	}
	if ev.GetMessage() != "Executing plan plan-xyz" {
		t.Errorf("Message = %q, want %q", ev.GetMessage(), "Executing plan plan-xyz")
	}
	if ev.GetPlanId() != "plan-xyz" {
		t.Errorf("PlanId = %q, want %q", ev.GetPlanId(), "plan-xyz")
	}
	if ev.GetRootPath() != "/data/Music" {
		t.Errorf("RootPath = %q, want %q", ev.GetRootPath(), "/data/Music")
	}
	if ev.GetFolderPath() != "/data/Music/AlbumA" {
		t.Errorf("FolderPath = %q, want %q", ev.GetFolderPath(), "/data/Music/AlbumA")
	}
	if ev.GetItemSourcePath() != "/data/Music/AlbumA/song.wav" {
		t.Errorf("ItemSourcePath = %q, want %q", ev.GetItemSourcePath(), "/data/Music/AlbumA/song.wav")
	}
	if ev.GetItemTargetPath() != "/data/Music/AlbumA/song.mp3" {
		t.Errorf("ItemTargetPath = %q, want %q", ev.GetItemTargetPath(), "/data/Music/AlbumA/song.mp3")
	}
	if ev.GetProgressPercent() != 50 {
		t.Errorf("ProgressPercent = %d, want %d", ev.GetProgressPercent(), 50)
	}
	if ev.GetEventId() != "evt-abc-123" {
		t.Errorf("EventId = %q, want %q", ev.GetEventId(), "evt-abc-123")
	}
	if ev.GetTimestamp() != refTime.Format(time.RFC3339Nano) {
		t.Errorf("Timestamp = %q, want %q", ev.GetTimestamp(), refTime.Format(time.RFC3339Nano))
	}
}

// TestExecutePlan_MultipleEventsMapping verifies that multiple usecase events
// are all mapped through the sink correctly.
func TestExecutePlan_MultipleEventsMapping(t *testing.T) {
	useCaseSvc := &fakeExecuteService{
		result: executeusecase.Result{PlanID: "plan-multi", Status: "completed"},
		events: []executeusecase.Event{
			{Type: "started", PlanID: "plan-multi", EventID: "evt-1"},
			{Type: "folder_completed", PlanID: "plan-multi", FolderPath: "/Album", EventID: "evt-2"},
			{Type: "completed", PlanID: "plan-multi", EventID: "evt-3", ProgressPercent: 100},
		},
	}
	server := &OnseiServer{executeService: useCaseSvc}
	stream := &mockServerStreamHelper{}

	err := server.ExecutePlan(&pb.ExecutePlanRequest{PlanId: "plan-multi"}, stream)
	if err != nil {
		t.Fatalf("ExecutePlan returned error: %v", err)
	}

	if len(stream.events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(stream.events))
	}

	expectedTypes := []string{"started", "folder_completed", "completed"}
	for i, want := range expectedTypes {
		if got := stream.events[i].GetEventType(); got != want {
			t.Errorf("event[%d].EventType = %q, want %q", i, got, want)
		}
		if got := stream.events[i].GetEventId(); got == "" {
			t.Errorf("event[%d].EventId is empty", i)
		}
	}
}

// TestExecutePlan_ZeroTimestampMappedWithoutPanic verifies that a usecase.Event
// with a zero time.Time value for Timestamp is mapped to a non-empty string
// (the RFC3339Nano representation of the zero time) rather than panicking or
// producing an empty Timestamp field.
func TestExecutePlan_ZeroTimestampMappedWithoutPanic(t *testing.T) {
	zeroTimeEvt := executeusecase.Event{
		Type:    "started",
		PlanID:  "plan-zero-ts",
		EventID: "evt-zero-ts",
		// Timestamp left at zero value.
	}
	useCaseSvc := &fakeExecuteService{
		result: executeusecase.Result{PlanID: "plan-zero-ts", Status: "ok"},
		events: []executeusecase.Event{zeroTimeEvt},
	}
	server := &OnseiServer{executeService: useCaseSvc}
	stream := &mockServerStreamHelper{}

	err := server.ExecutePlan(&pb.ExecutePlanRequest{PlanId: "plan-zero-ts"}, stream)
	if err != nil {
		t.Fatalf("ExecutePlan returned error: %v", err)
	}

	ev := stream.events[0]
	// Zero time must still produce a non-empty RFC3339Nano string.
	if got := ev.GetTimestamp(); got == "" {
		t.Error("Timestamp must not be empty for zero time.Time value")
	}
	// The zero time formatted with RFC3339Nano is "0001-01-01T00:00:00Z".
	wantZero := time.Time{}.Format(time.RFC3339Nano)
	if got := ev.GetTimestamp(); got != wantZero {
		t.Errorf("Timestamp = %q, want %q (zero time.Time formatted as RFC3339Nano)", got, wantZero)
	}
}
