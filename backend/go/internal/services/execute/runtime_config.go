package execute

import (
	"os"
	"path/filepath"
	"runtime"
)

// Prototype pipeline parameters (fixed for minimal implementation).
const (
	// Stage1Workers = 1 (copy to scratch)
	Stage1Workers = 1
	// Stage3Workers = 1 (commit from scratch to final destination)
	Stage3Workers = 1
)

// stage2Workers returns runtime.NumCPU().
func stage2Workers() int {
	n := runtime.NumCPU()
	return n
}

// defaultScratchRoot returns the default scratch directory path.
func defaultScratchRoot() string {
	return filepath.Join(os.TempDir(), "onsei-organizer", "temp")
}
