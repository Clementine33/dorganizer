package execute //nolint:testpackage // white-box tests exercise unexported internals

import (
	"path/filepath"
	"testing"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
)

// TestExecuteReturnsToolNotFoundCode tests that convert with missing tool returns TOOL_NOT_FOUND.
func TestExecuteReturnsToolNotFoundCode(t *testing.T) {
	// Use a non-existent qaac path to trigger TOOL_NOT_FOUND
	svc := NewService(ToolsConfig{Encoder: "qaac", QAACPath: "/nonexistent/qaac.exe"})
	tmp := t.TempDir()

	item := PlanItem{
		Type: ItemTypeConvert,
		Src:  filepath.Join(tmp, "test.mp3"),
		Dst:  filepath.Join(tmp, "test.m4a"),
	}

	err := svc.ExecuteItem(item, false)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	code := MapError(err)
	if code != errdomain.TOOL_NOT_FOUND {
		t.Errorf("expected domain code TOOL_NOT_FOUND but got %v", code)
	}
}
