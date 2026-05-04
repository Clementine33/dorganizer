package grpc

import (
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	executeusecase "github.com/onsei/organizer/backend/internal/usecase/execute"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// OnseiServer implements pb.OnseiServiceServer.
//
// NOTE: RPC implementations are split across server_*.go files to keep each
// concern small and maintainable while preserving initialization behavior.
type OnseiServer struct {
	pb.UnimplementedOnseiServiceServer

	repo           *sqlite.Repository
	configDir      string
	ffmpeg         string
	planService    planusecase.Service
	executeService executeusecase.Service
}

// NewOnseiServer creates a new OnseiServer with default service construction.
// This preserves backward compatibility with existing callers.
func NewOnseiServer(repo *sqlite.Repository, configDir string, ffmpegPath string) *OnseiServer {
	return NewOnseiServerWithServices(
		planusecase.NewService(repo, configDir),
		executeusecase.NewService(repo, configDir),
		repo,
		configDir,
		ffmpegPath,
	)
}

// NewOnseiServerWithServices creates a new OnseiServer with injected services.
// This enables clean dependency injection for testing and future wiring.
func NewOnseiServerWithServices(planService planusecase.Service, executeService executeusecase.Service, repo *sqlite.Repository, configDir string, ffmpegPath string) *OnseiServer {
	return &OnseiServer{
		repo:           repo,
		configDir:      configDir,
		ffmpeg:         ffmpegPath,
		planService:    planService,
		executeService: executeService,
	}
}
