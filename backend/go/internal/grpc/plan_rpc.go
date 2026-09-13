package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
)

// PlanOperations is legacy-plan surface. The workflow Plan model cannot be
// expressed by the current proto, and no legacy slim/prune compatibility
// layer is retained, so every PlanOperations request is rejected explicitly
// rather than silently deriving a silent default.
func (s *OnseiServer) PlanOperations(
	_ context.Context,
	_ *pb.PlanOperationsRequest,
) (*pb.PlanOperationsResponse, error) {
	return nil, status.Error(
		codes.InvalidArgument,
		"WORKFLOW_REQUIRED: PlanOperations does not support the workflow plan contract; use POST /api/v1/plans",
	)
}
