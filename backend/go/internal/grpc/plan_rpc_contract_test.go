package grpc

import (
	"context"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestPlanOperations_RejectedWorkflow guards the breaking migration: the
// legacy gRPC PlanOperations cannot express the workflow contract and no
// slim/prune compatibility layer is retained, so every request is rejected
// explicitly instead of silently deriving a default plan.
func TestPlanOperations_RejectedWorkflow(t *testing.T) {
	server := NewOnseiServerWithServices(nil, nil, nil, "", "")

	_, err := server.PlanOperations(context.Background(), &pb.PlanOperationsRequest{
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
		FolderPath:   "/folder",
	})
	if err == nil {
		t.Fatal("PlanOperations should return an error for the workflow contract")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}
	if st.Message() != "WORKFLOW_REQUIRED: PlanOperations does not support the workflow plan contract; use POST /api/v1/plans" {
		t.Fatalf("message = %q", st.Message())
	}
}
