package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
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

// mapPlanError maps a plan usecase error to a gRPC status error.
// This keeps status creation transport-only.
func mapPlanError(err error) error {
	if planErr, ok := planusecase.AsError(err); ok {
		switch planErr.Kind {
		case planusecase.ErrKindInvalidArgument:
			return status.Errorf(codes.InvalidArgument, "%s", planErr.Message)
		case planusecase.ErrKindAlreadyExists:
			return status.Errorf(codes.AlreadyExists, "%s", planErr.Message)
		}
		// ErrKindInternal and unknown kinds fall through to Internal
		return status.Errorf(codes.Internal, "%s", planErr.Message)
	}
	return status.Errorf(codes.Internal, "%v", err)
}
