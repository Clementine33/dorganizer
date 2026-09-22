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

	"github.com/onsei/organizer/backend/internal/adapters/filesystem"
	"github.com/onsei/organizer/backend/internal/adapters/httpapi"
	appconfig "github.com/onsei/organizer/backend/internal/adapters/settings"
	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/bootstrap"
	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/library"
	"github.com/onsei/organizer/backend/internal/maintenance"
	"github.com/onsei/organizer/backend/internal/services/fileops"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
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

// retentionWindows are how long the idle-time maintenance pass keeps finished
// session records: scans are disposable once their inventory has been merged,
// while a planning session is the record of a generation key and has to outlive
// the idempotency-key guarantee the workset service makes (ADR 0001, ADR 0008).
const (
	scanRetention       = 7 * 24 * time.Hour
	generationRetention = 30 * 24 * time.Hour
)

// logAutoVacuumMode says once whether this database can return freed pages to
// the filesystem at all. A file created before incremental auto-vacuum existed
// keeps them: the mode is fixed when the file is created, and reclamation
// cannot turn it on, so the operator has to point ONSEI_DATA_DIR somewhere new.
func logAutoVacuumMode(ctx context.Context, repo *sqlite.Repository) {
	mode, err := repo.AutoVacuumMode(ctx)
	if err != nil {
		log.Printf("auto_vacuum probe failed: %v", err)
		return
	}
	if mode != sqlite.AutoVacuumIncremental {
		log.Printf(
			"auto_vacuum=%d: reclaimed pages cannot be returned to the filesystem; "+
				"point ONSEI_DATA_DIR at a new directory for a database that can",
			mode,
		)
	}
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

	// Whether this database can give freed space back is worth one line at
	// startup: the answer is fixed when the file is created, and reclamation
	// cannot change it later (ADR 0008).
	logAutoVacuumMode(ctx, repo)

	// Build token (use env if provided, else empty)
	token := os.Getenv("ONSEI_TOKEN")

	runServer(ctx, repo, dataDir, configDir, ffmpegPath, token, version)
}

// services is the wired core: one gate, one scanning entry, one library entry
// and the workset service. It exists so the listener setup below reads as
// startup, not as assembly.
type services struct {
	gate    *admission.Gate
	library *library.Service
	scan    inventory.Service
	fileOps *fileops.Service
	workset worksetusecase.Service
}

// buildServices wires the process. Direct file management and the managed task
// paths share one admission control: a file operation is refused while a scan,
// planning session or execution is running, and starting one of those is
// refused while a file operation holds the slot (ADR 0002 §2).
//
// There is one scanning entry for the whole process — scanning a library,
// refreshing one member directory, and the refresh a session does before
// planning or writing all go through the same pipeline.
func buildServices(repo *sqlite.Repository, configDir string, generationConcurrency int) services {
	gate := admission.NewGate(repo.HasActiveSession)
	librarySvc := library.NewService(repo, gate)
	scanSvc := inventory.NewService(
		inventory.NewPipeline(
			sqlite.NewScanStaging(repo),
			filesystem.WalkRootEntriesParallel,
			filesystem.WalkFolderEntries,
		),
		repo,
		gate,
		librarySvc.RecordScanState,
	)
	// Encode concurrency stays automatic (min(4, CPU count)); a user setting
	// will feed this parameter later.
	//
	// Task registration: every kind a workset operation can carry. Creation
	// materializes one operation and seed draft per entry, in this order.
	//
	// Sessions refresh their member folders before planning and before writing:
	// the stored inventory is the only input fact a plan reads, and it is only
	// as current as the last scan.
	worksetSvc := worksetusecase.NewService(
		repo,
		generationConcurrency,
		0,
		[]worksetusecase.Task{tasksconversion.New(configDir)},
		scanSvc.RefreshMember,
		gate.Enqueue,
	)
	return services{
		gate:    gate,
		library: librarySvc,
		scan:    scanSvc,
		fileOps: fileops.NewService(gate, scanSvc.RefreshMember),
		workset: worksetSvc,
	}
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
	generationConcurrency := appconfig.DefaultAppConfig().Workset.GenerationConcurrency
	if cfg, err := os.ReadFile(filepath.Join(configDir, "config.json")); err == nil {
		var appCfg appconfig.AppConfig
		if json.Unmarshal(cfg, &appCfg) == nil && appCfg.Workset.GenerationConcurrency > 0 {
			generationConcurrency = appCfg.Workset.GenerationConcurrency
		}
	}
	services := buildServices(repo, configDir, generationConcurrency)

	// Startup recovery: any session left queued/running by a previous process
	// is marked interrupted before the dispatcher starts from an empty queue.
	interruptStaleSessions(repo)
	services.workset.DispatcherHandle().Start()
	defer services.workset.DispatcherHandle().Stop()

	// Idle-time maintenance shares the gate with everything that reads or writes
	// the trees and the database, so a pass and a scan, planning session or
	// execution never overlap. It runs for this process's lifetime only.
	maintenanceLoop := maintenance.New(repo, services.gate.BeginMaintenance, maintenance.Options{
		ScanRetention:       scanRetention,
		GenerationRetention: generationRetention,
	})
	// The startup pass runs to completion here, before the server accepts
	// anything: nothing else is alive yet, so it is the cheapest pass the
	// process will ever run, and no client can be refused by it.
	maintenanceLoop.Pass(ctx)
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		maintenanceLoop.Run(ctx)
	}()

	httpSrv := &http.Server{
		Handler: httpapi.NewServer(httpapi.Dependencies{
			Repo:           repo,
			Library:        services.library,
			ConfigDir:      configDir,
			Token:          token,
			CORSOrigins:    parseCORSOrigins(os.Getenv("ONSEI_CORS_ORIGINS")),
			Version:        version,
			Inventory:      services.scan,
			WorksetService: services.workset,
			FileOps:        services.fileOps,
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
	// The pass may be mid-statement; let it return before the repository it
	// reads from is closed by main.
	<-maintenanceDone
}

// interruptStaleSessions marks leftover queued/running sessions of a previous
// process as interrupted (releasing generation idempotency keys). Scan sessions
// are finalized here as well: their non-terminal states make
// HasActiveScanForRoot refuse planning and execution against the root, and
// retention deletes terminal rows only, so a leftover row would otherwise never
// clear. Execution sessions keep their partial report; nothing is resumed,
// re-encoded or re-deleted.
func interruptStaleSessions(repo *sqlite.Repository) {
	if err := repo.InterruptStaleGenerations(); err != nil {
		log.Printf("interrupt stale generations failed: %v", err)
	}
	if scans, err := repo.InterruptStaleScanSessions(); err != nil {
		log.Printf("interrupt stale scan sessions failed: %v", err)
	} else if scans > 0 {
		log.Printf("interrupted %d stale scan session(s)", scans)
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
