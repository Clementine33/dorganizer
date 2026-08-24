package grpc

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/services/scanner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Scan scans a folder and streams JobEvent progress.
func (s *OnseiServer) Scan(req *pb.ScanRequest, stream grpc.ServerStreamingServer[pb.JobEvent]) error {
	if req.FolderPath == "" {
		return status.Errorf(codes.InvalidArgument, "folder_path is required")
	}

	_ = stream.Send(&pb.JobEvent{EventType: "started", Message: fmt.Sprintf("Scanning %s", req.FolderPath)})

	svc := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(s.repo))
	scanID, err := svc.ScanRoot(req.FolderPath)
	if err != nil {
		_ = stream.Send(&pb.JobEvent{EventType: "error", Message: fmt.Sprintf("Scan failed: %v", err)})
		return nil
	}

	_ = stream.Send(&pb.JobEvent{
		EventType:       "completed",
		Message:         fmt.Sprintf("Scan completed (scan ID: %s)", scanID),
		ProgressPercent: 100,
	})
	return nil
}

// RefreshFolders performs folder-scoped scans for the given folder_paths under root_path.
func (s *OnseiServer) RefreshFolders(_ context.Context, req *pb.RefreshFoldersRequest) (*pb.RefreshFoldersResponse, error) {
	if req.GetRootPath() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "root_path is required")
	}
	if len(req.GetFolderPaths()) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "folder_paths is required")
	}

	rootPath := req.GetRootPath()
	folderPaths := req.GetFolderPaths()
	svc := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(s.repo))

	var successfulFolders []string
	var errors []*pb.FolderError
	timestamp := time.Now().Format(time.RFC3339Nano)

	for _, folderPath := range folderPaths {
		folderPathNorm := filepath.ToSlash(filepath.Clean(folderPath))
		_, err := svc.ScanFolder(folderPath, rootPath)
		if err != nil {
			errors = append(errors, &pb.FolderError{
				Stage:      "refresh",
				Code:       "SCAN_FOLDER_FAILED",
				Message:    fmt.Sprintf("failed to scan folder: %v", err),
				FolderPath: folderPathNorm,
				RootPath:   filepath.ToSlash(filepath.Clean(rootPath)),
				Timestamp:  timestamp,
				EventId:    generateEventID(),
			})
		} else {
			successfulFolders = append(successfulFolders, folderPathNorm)
		}
	}

	return &pb.RefreshFoldersResponse{SuccessfulFolders: successfulFolders, Errors: errors}, nil
}
