package grpc

import (
	"context"
	"os"
	"path/filepath"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetConfig returns the current configuration as JSON.
func (s *OnseiServer) GetConfig(_ context.Context, _ *pb.GetConfigRequest) (*pb.GetConfigResponse, error) {
	cfgPath := filepath.Join(s.configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &pb.GetConfigResponse{ConfigJson: "{}"}, nil
		}
		return nil, status.Errorf(codes.Internal, "read config: %v", err)
	}
	return &pb.GetConfigResponse{ConfigJson: string(data)}, nil
}

// UpdateConfig writes configuration JSON to disk.
func (s *OnseiServer) UpdateConfig(_ context.Context, req *pb.UpdateConfigRequest) (*pb.UpdateConfigResponse, error) {
	cfgPath := filepath.Join(s.configDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(req.ConfigJson), 0644); err != nil {
		return nil, status.Errorf(codes.Internal, "write config: %v", err)
	}
	return &pb.UpdateConfigResponse{}, nil
}
