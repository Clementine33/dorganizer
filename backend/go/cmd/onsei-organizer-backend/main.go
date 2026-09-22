// Command onsei-organizer-backend is the process entry point: it wires the
// process's signals and working directory and hands the rest to internal/app,
// which owns assembly and the process's lifetime.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/onsei/organizer/backend/internal/app"
)

// version is stamped at build time (-X main.version); it reaches the client
// through the startup handshake line.
var version = "dev"

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

	app.StartParentDeathWatchers(ctx, cancel, os.Stdin, os.Getppid(), nil)

	// Resolve the data directory relative to the executable, so the binary works
	// wherever it is unpacked; ONSEI_DATA_DIR overrides it.
	exePath, err := os.Executable()
	if err != nil {
		//nolint:gocritic // a process that cannot locate itself exits, deferred cleanup and all
		log.Fatalf("cannot find executable path: %v", err)
	}
	// Executable is at backend/go/bin/onsei-organizer-backend[.exe]; the data
	// directory is the project root, three levels up.
	dataDir := filepath.Join(filepath.Dir(exePath), "..", "..", "..")
	dataDir, _ = filepath.Abs(dataDir)
	if d := os.Getenv("ONSEI_DATA_DIR"); d != "" {
		dataDir = d
	}

	app.Run(ctx, app.Config{
		DataDir: dataDir,
		// The configuration file lives beside the database.
		ConfigDir:  dataDir,
		FFmpegPath: os.Getenv("ONSEI_FFMPEG"),
		Token:      os.Getenv("ONSEI_TOKEN"),
		Version:    version,
	})
}
