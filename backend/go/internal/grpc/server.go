package grpc

import (
	"path/filepath"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Package-level hooks for deterministic testing
var filepathAbs = filepath.Abs

// OnseiServer implements pb.OnseiServiceServer.
//
// NOTE: RPC implementations are split across server_*.go files to keep each
// concern small and maintainable while preserving initialization behavior.
type OnseiServer struct {
	pb.UnimplementedOnseiServiceServer

	repo      *sqlite.Repository
	configDir string
	ffmpeg    string
}

// NewOnseiServer creates a new OnseiServer.
func NewOnseiServer(repo *sqlite.Repository, configDir string, ffmpegPath string) *OnseiServer {
	return &OnseiServer{repo: repo, configDir: configDir, ffmpeg: ffmpegPath}
}
