package execute

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	exesvc "github.com/onsei/organizer/backend/internal/services/execute"
)

// Shared test helpers for execute usecase tests.

// testEventSink collects events for assertion in tests.
type testEventSink struct {
	events []Event
}

func (s *testEventSink) Emit(e Event) error {
	s.events = append(s.events, e)
	return nil
}

// folderEvents extracts events of the given type and returns their folder paths.
func folderEvents(events []Event, eventType string) []string {
	var paths []string
	for _, e := range events {
		if e.Type == eventType {
			paths = append(paths, e.FolderPath)
		}
	}
	return paths
}

func containsFolder(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

// eventTypes extracts all event types in order from a slice of events.
func eventTypes(events []Event) []string {
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	return types
}

type sentinelError struct{ msg string }

func (e sentinelError) Error() string { return e.msg }

var _ = exesvc.PlanItem{} // ensure import used

// fakeEncoderSource is a tiny native encoder: it copies the source argument to
// the output argument and exits 0, matching the lame encoder argument layout
// (-b 320 <src> <out>). It is compiled at test time so no shell script or
// platform-specific batch file is needed.
const fakeEncoderSource = `package main

import (
	"io"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) < 2 {
		os.Exit(1)
	}
	src, dst := args[len(args)-2], args[len(args)-1]
	in, err := os.Open(src)
	if err != nil {
		os.Exit(1)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		os.Exit(1)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		os.Exit(1)
	}
	if err := out.Close(); err != nil {
		os.Exit(1)
	}
}
`

// createFakeEncoder builds a deterministic fake encoder executable for tests.
// It copies src to dst and exits 0, ensuring the convert success path works on
// both Windows and Linux without relying on a batch interpreter.
func createFakeEncoder(t *testing.T, tmpDir string) string {
	t.Helper()
	srcFile := filepath.Join(tmpDir, "fake_encoder_main.go")
	if err := os.WriteFile(srcFile, []byte(fakeEncoderSource), 0o600); err != nil {
		t.Fatalf("failed to write fake encoder source: %v", err)
	}
	encoderPath := filepath.Join(tmpDir, "fake_lame")
	if runtime.GOOS == "windows" {
		encoderPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", encoderPath, srcFile)
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build fake encoder: %v\n%s", err, out)
	}
	return encoderPath
}

// findErrorEventByCode queries error_events directly for a given code, returning
// the first match. Used for cases where rootPath may be empty.
func findErrorEventByCode(t *testing.T, repo *sqlite.Repository, code string) *sqlite.ErrorEvent {
	t.Helper()
	row := repo.DB().QueryRow(
		`SELECT id, scope, root_path, path, code, message, retryable, created_at FROM error_events WHERE code = ? ORDER BY id DESC LIMIT 1`,
		code,
	)
	var e sqlite.ErrorEvent
	var retryable int
	var createdAtStr string
	var path sql.NullString
	if err := row.Scan(
		&e.ID,
		&e.Scope,
		&e.RootPath,
		&path,
		&e.Code,
		&e.Message,
		&retryable,
		&createdAtStr,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		t.Fatalf("query error_events by code %q: %v", code, err)
	}
	if path.Valid {
		e.Path = &path.String
	}
	e.Retryable = retryable == 1
	return &e
}

// seedEntry inserts a test entry into the entries table.
func seedEntry(t *testing.T, repo *sqlite.Repository, filePath, rootPath string, info os.FileInfo) {
	t.Helper()
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(filePath), filepath.ToSlash(rootPath), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}
}

// seedPlan creates a test plan row.
func seedPlan(t *testing.T, repo *sqlite.Repository, planID, rootPath, planType string) {
	t.Helper()
	now := time.Now()
	_, err := repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, ?, NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(rootPath), planType, now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}
}

// seedPlanWithScanRoot creates a test plan row with scan_root_path.
func seedPlanWithScanRoot(t *testing.T, repo *sqlite.Repository, planID, rootPath, scanRootPath, planType string) {
	t.Helper()
	now := time.Now()
	_, err := repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, ?, ?, NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(rootPath), filepath.ToSlash(scanRootPath), planType, now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}
}

// seedDeleteItem creates a plan item for a delete operation.
func seedDeleteItem(
	t *testing.T,
	repo *sqlite.Repository,
	planID string,
	itemIndex int,
	sourcePath, targetPath, precondPath string,
	contentRev, size int64,
	mtime int64,
) {
	t.Helper()
	targetVal := "NULL"
	if targetPath != "" {
		targetVal = "'" + filepath.ToSlash(targetPath) + "'"
	}
	precondVal := "NULL"
	if precondPath != "" {
		precondVal = "'" + filepath.ToSlash(precondPath) + "'"
	}
	_, err := repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, ?, 'delete', ?, `+targetVal+`, '', `+precondVal+`, ?, ?, ?)
	`, planID, itemIndex, filepath.ToSlash(sourcePath), contentRev, size, mtime)
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}
}

// seedConvertItem creates a plan item for a convert_and_delete operation.
func seedConvertItem(
	t *testing.T,
	repo *sqlite.Repository,
	planID string,
	itemIndex int,
	sourcePath, targetPath, precondPath string,
	contentRev, size int64,
	mtime int64,
) {
	t.Helper()
	_, err := repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, ?, 'convert_and_delete', ?, ?, '', ?, ?, ?, ?)
	`, planID, itemIndex, filepath.ToSlash(sourcePath), filepath.ToSlash(targetPath), filepath.ToSlash(precondPath), contentRev, size, mtime)
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}
}

// writeTestFile creates a test file with dummy content and returns its os.FileInfo.
func writeTestFile(t *testing.T, path string, content string) os.FileInfo {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// newTestRepo creates a new SQLite repository in the given temp directory.
func newTestRepo(t *testing.T, tmpDir string) *sqlite.Repository {
	t.Helper()
	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}
