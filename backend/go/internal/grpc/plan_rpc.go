package grpc

import (
	"context"
	"time"

	"github.com/google/uuid"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PlanOperations generates a plan for the given source files and target format.
// This is a thin adapter that maps protobuf requests/responses to the plan usecase.
func (s *OnseiServer) PlanOperations(ctx context.Context, req *pb.PlanOperationsRequest) (*pb.PlanOperationsResponse, error) {
	planReq := planusecase.Request{
		PlanType:             req.GetPlanType(),
		TargetFormat:         req.GetTargetFormat(),
		SourceFiles:          req.GetSourceFiles(),
		FolderPath:           req.GetFolderPath(),
		FolderPaths:          req.GetFolderPaths(),
		PruneMatchedExcluded: req.GetPruneMatchedExcluded(),
	}

	resp, err := s.planService.Plan(ctx, planReq)
	if err != nil {
		return nil, mapPlanError(err)
	}

	// Map usecase response to protobuf response — no outcome interpretation.
	timestamp := time.Now().Format(time.RFC3339Nano)

	ops := make([]*pb.PlannedOperation, 0, len(resp.Operations))
	for _, op := range resp.Operations {
		ops = append(ops, &pb.PlannedOperation{
			SourcePath:    op.SourcePath,
			TargetPath:    op.TargetPath,
			OperationType: op.Type,
		})
	}

	planErrors := make([]*pb.FolderError, 0, len(resp.Errors))
	for _, pe := range resp.Errors {
		planErrors = append(planErrors, &pb.FolderError{
			Stage:      "plan",
			Code:       pe.Code,
			Message:    pe.Message,
			FolderPath: pe.FolderPath,
			PlanId:     resp.PlanID,
			RootPath:   resp.RootPath,
			Timestamp:  timestamp,
			EventId:    newEventID(),
		})
	}

	return &pb.PlanOperationsResponse{
		PlanId:            resp.PlanID,
		Operations:        ops,
		TotalCount:        int32(resp.Summary.TotalCount),
		ActionableCount:   int32(resp.Summary.ActionableCount),
		SummaryReason:     resp.Summary.SummaryReason,
		PlanErrors:        planErrors,
		SuccessfulFolders: resp.SuccessfulFolders,
	}, nil
}

// newEventID generates a unique event identifier for proto FolderError entries.
// This is transport-layer mapping logic owned by the gRPC adapter.
func newEventID() string {
	return "evt-" + uuid.NewString()
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
