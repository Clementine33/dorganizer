package grpc

import (
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	executeusecase "github.com/onsei/organizer/backend/internal/usecase/execute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mapExecuteError maps an execute usecase error to a gRPC status error.
// This keeps status creation transport-only.
func mapExecuteError(err error) error {
	if e, ok := executeusecase.AsError(err); ok {
		switch e.Kind {
		case executeusecase.ErrKindInvalidArgument:
			return status.Errorf(codes.InvalidArgument, "%s", e.Message)
		case executeusecase.ErrKindNotFound:
			return status.Errorf(codes.NotFound, "%s", e.Message)
		case executeusecase.ErrKindFailedPrecondition:
			return status.Errorf(codes.FailedPrecondition, "%s", e.Message)
		}
		// ErrKindInternal and unknown kinds fall through to Internal
		return status.Errorf(codes.Internal, "%s", e.Message)
	}
	return status.Errorf(codes.Internal, "%v", err)
}

// ExecutePlan executes a saved plan and streams JobEvent progress.
// This is a thin transport adapter that maps protobuf requests/responses to the execute usecase.
func (s *OnseiServer) ExecutePlan(req *pb.ExecutePlanRequest, stream grpc.ServerStreamingServer[pb.JobEvent]) error {
	usecaseReq := executeusecase.Request{
		PlanID:     req.PlanId,
		SoftDelete: req.GetSoftDelete(),
	}

	sink := &grpcEventSink{stream: stream}

	result, err := s.executeService.Execute(stream.Context(), usecaseReq, sink)
	if err != nil {
		return mapExecuteError(err)
	}

	_ = result
	return nil
}

// grpcEventSink adapts a gRPC server stream to the usecase EventSink interface.
// It maps usecase.Event protobuf-free events to pb.JobEvent for streaming.
type grpcEventSink struct {
	stream grpc.ServerStreamingServer[pb.JobEvent]
}

// Emit sends a usecase event as a protobuf JobEvent through the gRPC stream.
func (s *grpcEventSink) Emit(evt executeusecase.Event) error {
	return s.stream.Send(&pb.JobEvent{
		EventType:       evt.Type,
		Stage:           evt.Stage,
		Code:            evt.Code,
		Message:         evt.Message,
		PlanId:          evt.PlanID,
		RootPath:        evt.RootPath,
		FolderPath:      evt.FolderPath,
		ItemSourcePath:  evt.ItemSourcePath,
		ItemTargetPath:  evt.ItemTargetPath,
		ProgressPercent: evt.ProgressPercent,
		EventId:         evt.EventID,
		Timestamp:       evt.Timestamp.Format(time.RFC3339Nano),
	})
}
