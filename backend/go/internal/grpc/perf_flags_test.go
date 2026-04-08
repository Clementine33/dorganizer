package grpc

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func TestPerfFlags_Defaults_AreSafe(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	planCfg, err := server.getPlanConfig()
	if err != nil {
		t.Fatalf("getPlanConfig failed: %v", err)
	}
	if !planCfg.Slim.RequireScope {
		t.Fatal("expected default plan.slim.require_scope=true")
	}
	if !planCfg.RootResolve.Batch {
		t.Fatal("expected default plan.root_resolve.batch=true")
	}
	if !planCfg.Bitrate.BatchUpdate {
		t.Fatal("expected default plan.bitrate.batch_update=true")
	}

	execCfg, err := server.getExecuteConfig()
	if err != nil {
		t.Fatalf("getExecuteConfig failed: %v", err)
	}
	if execCfg.PrecheckConcurrentStat {
		t.Fatal("expected default execute.precheck.concurrent_stat=false")
	}
}

func TestPerfFlags_ExecutePrecheckConcurrentStat_OverrideTrue(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	configJSON := `{
		"execute": {
			"precheck": {
				"concurrent_stat": true
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	execCfg, err := server.getExecuteConfig()
	if err != nil {
		t.Fatalf("getExecuteConfig failed: %v", err)
	}
	if !execCfg.PrecheckConcurrentStat {
		t.Fatal("expected execute.precheck.concurrent_stat=true from config override")
	}
}

func TestPlanOperations_Slim_NoScope_RequireScopeDisabled_AllowsLegacyGlobalPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	mp3 := filepath.Join(tmpDir, "song.mp3")
	flac := filepath.Join(tmpDir, "song.flac")
	if err := os.WriteFile(mp3, []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create mp3: %v", err)
	}
	if err := os.WriteFile(flac, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create flac: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, 192000)
	`, filepath.ToSlash(mp3), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert mp3: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(flac), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert flac: %v", err)
	}

	configJSON := `{
		"plan": {
			"slim": {
				"require_scope": false
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{PlanType: "slim"})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	for _, pe := range resp.GetPlanErrors() {
		if pe.GetCode() == "GLOBAL_NO_SCOPE" {
			t.Fatalf("expected no GLOBAL_NO_SCOPE when require_scope=false, got response: %+v", resp)
		}
	}
}
