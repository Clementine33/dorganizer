package execute

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetExecuteConfig_ParseErrorReturnsError verifies that malformed config
// returns an error alongside the fallback config.
func TestGetExecuteConfig_ParseErrorReturnsError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-exec-cfg-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write malformed JSON config
	configJSON := `{ "execute": { "max_io_workers": 8, broken }`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, cfgErr := getExecuteConfig(tmpDir)
	if cfgErr == nil {
		t.Fatal("expected parse error from malformed config, got nil")
	}
	// Fallback config should still be usable
	if cfg.MaxIOWorkers < 1 || cfg.MaxIOWorkers > 1000 {
		t.Fatalf("expected usable fallback config, got %+v", cfg)
	}
}

// TestGetExecuteConfig_NotExistReturnsNilError verifies that missing config
// returns no error (not-found is tolerated).
func TestGetExecuteConfig_NotExistReturnsNilError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-no-cfg-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg, cfgErr := getExecuteConfig(tmpDir)
	if cfgErr != nil {
		t.Fatalf("expected no error for missing config, got: %v", cfgErr)
	}
	if cfg.MaxIOWorkers < 1 || cfg.MaxIOWorkers > 1000 {
		t.Fatalf("expected usable default config, got %+v", cfg)
	}
}
