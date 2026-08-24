package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/onsei/organizer/backend/internal/bootstrap"
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	grpcimpl "github.com/onsei/organizer/backend/internal/grpc"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"google.golang.org/grpc"
)

var version = "dev"

// retentionCleaner abstracts the repo for startup cleanup so main_test.go can stub it.
type retentionCleaner interface {
	RunRetentionCleanup(cutoff time.Time) (sqlite.CleanupStats, error)
}

// runStartupRetentionCleanup performs a one-time retention cleanup at startup.
// It is non-fatal: the returned error is logged but does not stop the process.
func runStartupRetentionCleanup(repo retentionCleaner, now time.Time) error {
	cutoff := now.UTC().Add(-7 * 24 * time.Hour)
	start := time.Now()
	stats, err := repo.RunRetentionCleanup(cutoff)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	log.Printf("startup retention cleanup: deleted error_events=%d scan_sessions=%d plans=%d cutoff=%s elapsed_ms=%d",
		stats.DeletedErrorEvents, stats.DeletedScanSessions, stats.DeletedPlans,
		cutoff.Format(time.RFC3339), elapsed.Milliseconds())
	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	startParentDeathWatchers(ctx, cancel, os.Stdin, os.Getppid(), nil)

	// Resolve config/data directory relative to the executable
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("cannot find executable path: %v", err)
	}
	// Executable is at backend/go/bin/onsei-organizer-backend.exe
	// Data dir is 3 levels up (project root)
	exeDir := filepath.Dir(exePath)
	dataDir := filepath.Join(exeDir, "..", "..", "..")
	dataDir, _ = filepath.Abs(dataDir)

	// Allow overriding via env
	if d := os.Getenv("ONSEI_DATA_DIR"); d != "" {
		dataDir = d
	}

	dbPath := filepath.Join(dataDir, "cache.db")
	configDir := dataDir
	ffmpegPath := "ffmpeg" // rely on PATH; override with ONSEI_FFMPEG env

	if f := os.Getenv("ONSEI_FFMPEG"); f != "" {
		ffmpegPath = f
	}

	// Ensure DB directory exists
	if err := sqlite.EnsureDBPath(dbPath); err != nil {
		log.Fatalf("ensure db path: %v", err)
	}

	// Open repository
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		log.Fatalf("open repository: %v", err)
	}
	defer repo.Close()

	// Route std logger to stdout so host-side stdout drain also covers logs.
	log.SetOutput(os.Stdout)

	// One-time startup retention cleanup (non-fatal)
	if err := runStartupRetentionCleanup(repo, time.Now()); err != nil {
		log.Printf("retention cleanup failed: %v", err)
	}

	// Start TCP listener on a random available port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port

	// Build token (use env if provided, else empty)
	token := os.Getenv("ONSEI_TOKEN")

	// Register gRPC server
	grpcServer := grpc.NewServer()
	srv := grpcimpl.NewOnseiServer(repo, configDir, ffmpegPath)
	pb.RegisterOnseiServiceServer(grpcServer, srv)
	startPprofServer("127.0.0.1:6060", http.ListenAndServe)

	var gracefulStopOnce sync.Once
	gracefulStop := func() {
		gracefulStopOnce.Do(func() {
			log.Printf("shutdown requested: draining gRPC server")

			forcedExit := time.AfterFunc(5*time.Second, func() {
				log.Printf("forced shutdown timeout reached")
				os.Exit(1)
			})
			defer forcedExit.Stop()

			grpcServer.GracefulStop()
		})
	}

	go func() {
		<-ctx.Done()
		gracefulStop()
	}()

	// Print ready handshake BEFORE blocking — Flutter reads this line
	fmt.Println(bootstrap.BuildHandshakeLine(port, token, version))

	// Block until killed
	log.Printf("onsei-backend listening on 127.0.0.1:%d (data=%s)", port, dataDir)
	if err := grpcServer.Serve(lis); err != nil {
		if runtime.GOOS == "windows" {
			const wsacancelled = 10004
			if opErr, ok := err.(*net.OpError); ok {
				if sysErr, ok := opErr.Err.(*os.SyscallError); ok {
					if errno, ok := sysErr.Err.(syscall.Errno); ok && int(errno) == wsacancelled {
						return
					}
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		log.Fatalf("serve: %v", err)
	}
}

func startPprofServer(addr string, serveFn func(string, http.Handler) error) {
	go func() {
		if err := serveFn(addr, nil); err != nil {
			log.Printf("pprof server exited: %v", err)
		}
	}()
}
