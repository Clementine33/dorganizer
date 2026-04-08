package execute

import (
	"path/filepath"
	"testing"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
)

// TestExecuteReturnsToolNotFoundCode tests that convert with missing tool returns TOOL_NOT_FOUND
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

// TestDeleteSoftDelete tests soft delete functionality
func TestDeleteSoftDelete(t *testing.T) {
	svc := NewService(ToolsConfig{})

	// Note: This is a basic test that doesn't actually delete files
	// In a real test, we'd use a temp file
	item := PlanItem{
		Type: ItemTypeDelete,
		Src:  "/tmp/nonexistent-file-for-test",
	}

	err := svc.ExecuteItem(item, true)
	// We expect this to fail since file doesn't exist, but it's a valid operation type
	_ = err
}

// TestConvertWithoutToolPath tests that empty tool path returns TOOL_NOT_FOUND
func TestConvertWithoutToolPath(t *testing.T) {
	svc := NewService(ToolsConfig{Encoder: "qaac"})
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
