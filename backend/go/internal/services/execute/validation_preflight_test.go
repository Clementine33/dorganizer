package execute //nolint:testpackage // white-box tests exercise unexported internals

import (
	"os"
	"path/filepath"
	"testing"
)

// TestToolsConfig_InvalidFFmpeg_Fails validates that invalid encoder fails.
//
//nolint:dupl // distinct encoder-validation scenarios
func TestToolsConfig_InvalidFFmpeg_Fails(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-invalid-encoder",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.m4a"),
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	// Invalid encoder should fail validation
	toolsConfig := ToolsConfig{
		FFmpegPath: "/nonexistent/ffmpeg",
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for invalid encoder, got nil")
	}
	if !containsString(execErr.Error(), "tool unavailable") {
		t.Fatalf("expected error message to contain 'invalid encoder', got: %v", execErr)
	}
}

// TestToolsConfig_FFmpeg_MissingPath_FailsBeforeItemLoop validates a missing ffmpeg path fails before item loop.
func TestToolsConfig_FFmpeg_MissingPath_FailsBeforeItemLoop(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-ffmpeg-missing",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.m4a"),
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	// Unconfigured paths fall back to an empty PATH and must fail at preflight
	toolsConfig := ToolsConfig{}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for missing ffmpeg path, got nil")
	}
	if !containsString(execErr.Error(), "ffmpeg") {
		t.Fatalf("expected error message to mention 'ffmpeg', got: %v", execErr)
	}
}

// TestToolsConfig_FFprobe_MissingPath_FailsBeforeItemLoop validates a missing ffprobe path fails before item loop.
func TestToolsConfig_FFprobe_MissingPath_FailsBeforeItemLoop(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-ffprobe-missing",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.mp3"),
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	// ffmpeg adapter without ffprobe_path should fail at preflight validation
	toolsConfig := ToolsConfig{
		FFmpegPath: getValidExecutablePath(t),
		// FFprobePath uses PATH, which is empty
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for missing ffprobe_path, got nil")
	}
	if !containsString(execErr.Error(), "ffprobe") {
		t.Fatalf("expected error message to mention 'ffprobe', got: %v", execErr)
	}
}

// TestToolsConfig_FFmpeg_InvalidPath_FailsBeforeItemLoop validates an invalid ffmpeg path fails before item loop.
//
//nolint:dupl // distinct invalid-path scenarios
func TestToolsConfig_FFmpeg_InvalidPath_FailsBeforeItemLoop(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-ffmpeg-invalid",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.m4a"),
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	// Invalid ffmpeg path should fail at preflight validation
	toolsConfig := ToolsConfig{
		FFmpegPath: "/nonexistent/ffmpeg",
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for invalid ffmpeg path, got nil")
	}
	if !containsString(execErr.Error(), "ffmpeg") {
		t.Fatalf("expected error message to mention 'ffmpeg', got: %v", execErr)
	}
}

// TestToolsConfig_FFprobe_InvalidPath_FailsBeforeItemLoop validates an invalid ffprobe path fails before item loop.
func TestToolsConfig_FFprobe_InvalidPath_FailsBeforeItemLoop(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-ffprobe-invalid",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.mp3"),
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	// ffmpeg adapter with invalid ffprobe_path should fail at preflight validation
	toolsConfig := ToolsConfig{
		FFmpegPath:  getValidExecutablePath(t),
		FFprobePath: "/nonexistent/ffprobe",
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for invalid ffprobe_path, got nil")
	}
	if !containsString(execErr.Error(), "ffprobe") {
		t.Fatalf("expected error message to mention 'ffprobe', got: %v", execErr)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStringHelper(s, substr))
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestDeleteOnlyPlan_SkipsToolsConfigValidation validates delete-only plans don't require tools config.
func TestDeleteOnlyPlan_SkipsToolsConfigValidation(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Delete-only plan with NO encoder configured
	plan := &Plan{
		PlanID: "plan-delete-only",
		Items: []PlanItem{{
			Type:                   ItemTypeDelete,
			SourcePath:             testFile,
			TargetPath:             "",
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	// Empty tools config - should NOT fail for delete-only plans
	toolsConfig := ToolsConfig{}

	svc := NewExecuteService(nil, toolsConfig)
	result, execErr := svc.ExecutePlan(plan)
	if execErr != nil {
		t.Fatalf("delete-only plan should not require tools config, got error: %v", execErr)
	}
	if result.Status != "completed" {
		t.Fatalf("expected status completed, got %s", result.Status)
	}
}

// TestConvertPlan_MissingTools_FailsPreflight validates convert plans fail at preflight if the tools are unset.
func TestConvertPlan_MissingTools_FailsPreflight(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Convert plan with no tools configured
	plan := &Plan{
		PlanID: "plan-convert-missing-tools",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.m4a"),
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	// Unresolvable tools should fail at preflight (not runtime)
	toolsConfig := ToolsConfig{}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for missing tools, got nil")
	}
	if !containsString(execErr.Error(), "tool unavailable") {
		t.Fatalf("expected error message to contain 'tool unavailable', got: %v", execErr)
	}
	// Verify source file was NOT deleted (mutation was blocked)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to exist - preflight should fail before any mutation")
	}
}

// TestConvertPlan_TargetExtensionMismatch_FailsPreflight validates convert plan target extension
// must be one the adapter can encode.
func TestConvertPlan_TargetExtensionMismatch_FailsPreflight(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-target-ext-mismatch",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.ogg"), // unsupported target
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	svc := NewExecuteService(nil, validToolsConfig(t))
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected target extension mismatch error, got nil")
	}
	if !containsString(execErr.Error(), "target extension") {
		t.Fatalf("expected error message to contain 'target extension', got: %v", execErr)
	}
	if !containsString(execErr.Error(), ".ogg") {
		t.Fatalf("expected error message to mention the offending '.ogg', got: %v", execErr)
	}
}
