package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onsei/organizer/backend/internal/bootstrap"
	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/httpapi"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/scanner"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
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
		"startup retention cleanup: deleted scan_sessions=%d generations=%d cutoff=%s elapsed_ms=%d",
		stats.DeletedScanSessions,
		stats.DeletedGenerations,
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
	if ensureErr := sqlite.EnsureDBPath(dbPath); ensureErr != nil {
		log.Fatalf("ensure db path: %v", ensureErr)
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
	if cleanupErr := runStartupRetentionCleanup(repo, time.Now()); cleanupErr != nil {
		log.Printf("retention cleanup failed: %v", cleanupErr)
	}

	// Build token (use env if provided, else empty)
	token := os.Getenv("ONSEI_TOKEN")

	runServer(ctx, repo, dataDir, configDir, ffmpegPath, token, version)
}

// runServer starts the HTTP listener and blocks until the process is killed.
// Startup failures are fatal (log.Fatalf) so CI/dev surfaces them.
func runServer(
	ctx context.Context,
	repo *sqlite.Repository,
	dataDir, configDir, ffmpegPath, token, version string,
) {
	startPprofServer("127.0.0.1:6060", http.ListenAndServe)

	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("http listen: %v", err)
	}
	httpPort := httpListener.Addr().(*net.TCPAddr).Port

	// The HTTP API is the only client surface. The workset service owns the
	// async planning dispatcher.
	scanSvc := scanusecase.NewService(repo)
	generationConcurrency := appconfig.DefaultAppConfig().Workset.GenerationConcurrency
	if cfg, err := os.ReadFile(filepath.Join(configDir, "config.json")); err == nil {
		var appCfg appconfig.AppConfig
		if json.Unmarshal(cfg, &appCfg) == nil && appCfg.Workset.GenerationConcurrency > 0 {
			generationConcurrency = appCfg.Workset.GenerationConcurrency
		}
	}
	// Task registration: every kind a workset operation can carry. Creation
	// materializes one operation and seed draft per entry, in this order.
	//
	// Sessions refresh their member folders through the scanner before planning
	// and before writing: the stored inventory is the only input fact a plan
	// reads, and it is only as current as the last scan.
	memberScanner := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(repo))
	worksetSvc := worksetusecase.NewService(repo, generationConcurrency, []worksetusecase.Task{
		tasksconversion.New(configDir),
	}, func(scanCtx context.Context, folderPath, rootPath string) error {
		_, scanErr := memberScanner.ScanFolderCtx(scanCtx, folderPath, rootPath)
		return scanErr
	})

	// Startup recovery: any session left queued/running by a previous process
	// is marked interrupted before the dispatcher starts from an empty queue.
	interruptStaleSessions(repo)
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
			log.Printf("shutdown requested: draining the HTTP server")

			forcedExit := time.AfterFunc(5*time.Second, func() {
				log.Printf("forced shutdown timeout reached")
				os.Exit(1)
			})
			defer forcedExit.Stop()

			// Drained with a deadline; the outer forced-exit guard remains as
			// the final process-level fallback.
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer shutdownCancel()
			drainHTTPServer(shutdownCtx, httpSrv.Shutdown, httpSrv.Close)
		})
	}

	go func() {
		<-ctx.Done()
		gracefulStop()
	}()

	// Print the ready handshake BEFORE blocking — the dev and e2e launchers
	// read this line to learn the HTTP port
	//nolint:forbidigo // stdout handshake is a wire protocol for the host
	fmt.Println(bootstrap.BuildHandshakeLine(token, version, httpPort))

	// Block until killed
	log.Printf("onsei-backend listening on http 127.0.0.1:%d (data=%s)", httpPort, dataDir)
	<-ctx.Done()
	gracefulStop()
}

// interruptStaleSessions marks leftover queued/running sessions of a previous
// process as interrupted (releasing generation idempotency keys). Execution
// sessions keep their partial report; nothing is resumed, re-encoded or
// re-deleted.
func interruptStaleSessions(repo *sqlite.Repository) {
	if err := repo.InterruptStaleGenerations(); err != nil {
		log.Printf("interrupt stale generations failed: %v", err)
	}
	n, err := repo.InterruptStaleExecutions()
	if err != nil {
		log.Printf("interrupt stale executions failed: %v", err)
		return
	}
	if n > 0 {
		log.Printf("interrupted %d stale execution session(s)", n)
	}
}

// drainHTTPServer shuts the HTTP server down gracefully; at the deadline (ctx
// done) it force-closes so the drain always returns.
func drainHTTPServer(
	ctx context.Context,
	httpShutdown func(context.Context) error,
	httpClose func() error,
) {
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := httpShutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	})
	go func() {
		<-ctx.Done()
		_ = httpClose()
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
