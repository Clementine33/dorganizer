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

// stage2Workers returns min(4, max(1, runtime.NumCPU()/2)).
func stage2Workers() int {
	n := runtime.NumCPU() / 2
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	return n
}

// defaultScratchRoot returns the default scratch directory path.
func defaultScratchRoot() string {
	return filepath.Join(os.TempDir(), "onsei-organizer", "temp")
}
