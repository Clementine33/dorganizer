package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/onsei/organizer/backend/internal/bootstrap"
	appconfig "github.com/onsei/organizer/backend/internal/config"
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	grpcimpl "github.com/onsei/organizer/backend/internal/grpc"
	"github.com/onsei/organizer/backend/internal/httpapi"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

var version = "dev"

// defaultCORSOrigins is the fallback ONSEI_CORS_ORIGINS allowlist: the local
// Vite dev servers from the Vue prototype.
const defaultCORSOrigins = "http://localhost:5173,http://127.0.0.1:5173"

// parseCORSOrigins splits a comma-separated allowlist into a slice, trimming
// whitespace and dropping empty segments. An empty input yields the defaults.
func parseCORSOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		raw = defaultCORSOrigins
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// retentionCleaner abstracts the repo for startup cleanup so main_test.go can stub it.
type retentionCleaner interface {
	RunRetentionCleanupWithCutoffs(cutoff, generationCutoff time.Time) (sqlite.CleanupStats, error)
}

// runStartupRetentionCleanup performs a one-time retention cleanup at startup.
// It is non-fatal: the returned error is logged but does not stop the process.
func runStartupRetentionCleanup(repo retentionCleaner, now time.Time) error {
	cutoff := now.UTC().Add(-7 * 24 * time.Hour)
	generationCutoff := now.UTC().Add(-30 * 24 * time.Hour)
	start := time.Now()
	stats, err := repo.RunRetentionCleanupWithCutoffs(cutoff, generationCutoff)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	log.Printf(
		"startup retention cleanup: deleted error_events=%d scan_sessions=%d generations=%d plans=%d cutoff=%s elapsed_ms=%d",
		stats.DeletedErrorEvents,
		stats.DeletedScanSessions,
		stats.DeletedGenerations,
		stats.DeletedPlans,
		cutoff.Format(time.RFC3339),
		elapsed.Milliseconds(),
	)
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

	// HTTP listener beside gRPC: same repository and usecase instances, so
	// browser and Flutter clients share one SQLite writer and one planner.
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("http listen: %v", err)
	}
	httpPort := httpListener.Addr().(*net.TCPAddr).Port

	// Shared scan/plan/workset usecases power both the gRPC server and the
	// HTTP API. The workset service owns the async planning dispatcher.
	scanSvc := scanusecase.NewService(repo)
	planSvc := planusecase.NewService(repo, configDir)
	generationConcurrency := appconfig.DefaultAppConfig().Workset.GenerationConcurrency
	if cfg, err := os.ReadFile(filepath.Join(configDir, "config.json")); err == nil {
		var appCfg appconfig.AppConfig
		if json.Unmarshal(cfg, &appCfg) == nil && appCfg.Workset.GenerationConcurrency > 0 {
			generationConcurrency = appCfg.Workset.GenerationConcurrency
		}
	}
	worksetSvc := worksetusecase.NewService(repo, configDir, generationConcurrency)

	// Startup recovery: any session left queued/running by a previous process
	// is marked interrupted (releasing its idempotency key) before the
	// dispatcher starts from an empty queue.
	if err := repo.InterruptStaleGenerations(); err != nil {
		log.Printf("interrupt stale generations failed: %v", err)
	}
	worksetSvc.DispatcherHandle().Start()
	defer worksetSvc.DispatcherHandle().Stop()

	httpSrv := &http.Server{
		Handler: httpapi.NewServer(httpapi.Dependencies{
			Repo:           repo,
			ConfigDir:      configDir,
			Token:          token,
			CORSOrigins:    parseCORSOrigins(os.Getenv("ONSEI_CORS_ORIGINS")),
			Version:        version,
			ScanService:    scanSvc,
			PlanService:    planSvc,
			WorksetService: worksetSvc,
		}),
	}
	go func() {
		if err := httpSrv.Serve(httpListener); err != nil && err != http.ErrServerClosed && ctx.Err() == nil {
			log.Fatalf("http serve: %v", err)
		}
	}()

	var gracefulStopOnce sync.Once
	gracefulStop := func() {
		gracefulStopOnce.Do(func() {
			log.Printf("shutdown requested: draining HTTP and gRPC servers")

			forcedExit := time.AfterFunc(5*time.Second, func() {
				log.Printf("forced shutdown timeout reached")
				os.Exit(1)
			})
			defer forcedExit.Stop()

			// Drain HTTP and gRPC concurrently so gRPC gets the full graceful
			// window instead of whatever remains after HTTP's own timeout. At
			// the graceful deadline both drains are force-stopped; the outer
			// forced-exit guard remains as the final process-level fallback.
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer shutdownCancel()
			drainServers(shutdownCtx, httpSrv.Shutdown, httpSrv.Close, grpcServer.GracefulStop, grpcServer.Stop)
		})
	}

	go func() {
		<-ctx.Done()
		gracefulStop()
	}()

	// Print ready handshake BEFORE blocking — Flutter reads this line
	fmt.Println(bootstrap.BuildHandshakeLine(port, token, version, httpPort))

	// Block until killed
	log.Printf("onsei-backend listening on grpc 127.0.0.1:%d, http 127.0.0.1:%d (data=%s)", port, httpPort, dataDir)
	if err := grpcServer.Serve(lis); err != nil {
		if runtime.GOOS == "windows" {
			const wsacancelled = 10004
			opErr := &net.OpError{}
			if errors.As(err, &opErr) {
				sysErr := &os.SyscallError{}
				if errors.As(opErr.Err, &sysErr) {
					var errno syscall.Errno
					if errors.As(sysErr.Err, &errno) {
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

// drainServers shuts down both servers concurrently so neither consumes the
// other's graceful window. httpShutdown runs with ctx; at the deadline (ctx
// done) httpClose and grpcStop force-stop each server so the drain returns.
func drainServers(
	ctx context.Context,
	httpShutdown func(context.Context) error,
	httpClose func() error,
	grpcGraceful func(),
	grpcStop func(),
) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := httpShutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		grpcGraceful()
	}()
	go func() {
		<-ctx.Done()
		_ = httpClose()
		grpcStop()
	}()
	wg.Wait()
}

func startPprofServer(addr string, serveFn func(string, http.Handler) error) {
	go func() {
		if err := serveFn(addr, nil); err != nil {
			log.Printf("pprof server exited: %v", err)
		}
	}()
}
