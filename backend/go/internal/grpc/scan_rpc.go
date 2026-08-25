package grpc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/services/scanner"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Scan scans a folder and streams JobEvent progress.
func (s *OnseiServer) Scan(req *pb.ScanRequest, stream grpc.ServerStreamingServer[pb.JobEvent]) error {
	if req.FolderPath == "" {
		return status.Errorf(codes.InvalidArgument, "folder_path is required")
	}

	service := scanusecase.NewService(s.repo)
	_, err := service.Scan(stream.Context(), scanusecase.Request{RootPath: req.FolderPath}, func(ev scanusecase.Event) {
		jobEvent := &pb.JobEvent{EventType: ev.Type, Message: ev.Message}
		switch ev.Type {
		case "completed":
			jobEvent.ProgressPercent = 100
		}
		_ = stream.Send(jobEvent)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return status.Errorf(codes.Canceled, "scan canceled")
		}
		if scanErr, ok := scanusecase.AsError(err); ok && scanErr.Kind == scanusecase.ErrKindInvalidArgument {
			return status.Error(codes.InvalidArgument, scanErr.Message)
		}
		// The error event was already streamed by the usecase; keep the
		// stream open (compatibility with previous behavior).
		return nil
	}
	return nil
}

// RefreshFolders performs folder-scoped scans for the given folder_paths under root_path.
func (s *OnseiServer) RefreshFolders(ctx context.Context, req *pb.RefreshFoldersRequest) (*pb.RefreshFoldersResponse, error) {
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
		_, err := svc.ScanFolderCtx(ctx, folderPath, rootPath)
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
