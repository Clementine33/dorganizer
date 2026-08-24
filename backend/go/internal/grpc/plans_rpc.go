package grpc

import (
	"context"
	"path/filepath"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListPlans returns plans for a given root_path, ordered by created_at DESC.
func (s *OnseiServer) ListPlans(_ context.Context, req *pb.ListPlansRequest) (*pb.ListPlansResponse, error) {
	rootPath := req.GetRootPath()
	if rootPath == "" {
		return nil, status.Errorf(codes.InvalidArgument, "root_path is required")
	}

	limit := req.GetLimit()
	if limit <= 0 {
		limit = 10
	}

	rootPathPosix := filepath.ToSlash(rootPath)
	plans, err := s.repo.ListPlansByRoot(rootPathPosix)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list plans: %v", err)
	}

	if len(plans) > int(limit) {
		plans = plans[:limit]
	}

	var planInfos []*pb.PlanInfo
	for _, p := range plans {
		planInfos = append(planInfos, &pb.PlanInfo{
			PlanId:    p.PlanID,
			RootPath:  p.RootPath,
			PlanType:  p.PlanType,
			Status:    p.Status,
			CreatedAt: p.CreatedAt.Format(time.RFC3339),
		})
	}

	return &pb.ListPlansResponse{Plans: planInfos}, nil
}
