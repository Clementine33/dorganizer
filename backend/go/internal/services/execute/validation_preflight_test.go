package execute

import (
	"os"
	"path/filepath"
	"testing"
)

// TestToolsConfig_InvalidEncoder_Fails validates that invalid encoder fails
func TestToolsConfig_InvalidEncoder_Fails(t *testing.T) {
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
		Encoder: "invalid-encoder",
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for invalid encoder, got nil")
	}
	if !containsString(execErr.Error(), "invalid encoder") {
		t.Fatalf("expected error message to contain 'invalid encoder', got: %v", execErr)
	}
}

// TestToolsConfig_QAAC_MissingPath_FailsBeforeItemLoop validates qaac selected but qaac_path missing fails before item loop
func TestToolsConfig_QAAC_MissingPath_FailsBeforeItemLoop(t *testing.T) {
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
		PlanID: "plan-qaac-missing",
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

	// qaac encoder without qaac_path should fail at preflight validation
	toolsConfig := ToolsConfig{
		Encoder: "qaac",
		// QAACPath is missing
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for missing qaac_path, got nil")
	}
	if !containsString(execErr.Error(), "qaac") {
		t.Fatalf("expected error message to mention 'qaac', got: %v", execErr)
	}
}

// TestToolsConfig_LAME_MissingPath_FailsBeforeItemLoop validates lame selected but lame_path missing fails before item loop
func TestToolsConfig_LAME_MissingPath_FailsBeforeItemLoop(t *testing.T) {
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
		PlanID: "plan-lame-missing",
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

	// lame encoder without lame_path should fail at preflight validation
	toolsConfig := ToolsConfig{
		Encoder: "lame",
		// LAMEPath is missing
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for missing lame_path, got nil")
	}
	if !containsString(execErr.Error(), "lame") {
		t.Fatalf("expected error message to mention 'lame', got: %v", execErr)
	}
}

// TestToolsConfig_QAAC_InvalidPath_FailsBeforeItemLoop validates qaac selected but qaac_path invalid fails before item loop
func TestToolsConfig_QAAC_InvalidPath_FailsBeforeItemLoop(t *testing.T) {
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
		PlanID: "plan-qaac-invalid",
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

	// qaac encoder with invalid qaac_path should fail at preflight validation
	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: "/nonexistent/qaac.exe",
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for invalid qaac_path, got nil")
	}
	if !containsString(execErr.Error(), "qaac") {
		t.Fatalf("expected error message to mention 'qaac', got: %v", execErr)
	}
}

// TestToolsConfig_LAME_InvalidPath_FailsBeforeItemLoop validates lame selected but lame_path invalid fails before item loop
func TestToolsConfig_LAME_InvalidPath_FailsBeforeItemLoop(t *testing.T) {
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
		PlanID: "plan-lame-invalid",
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

	// lame encoder with invalid lame_path should fail at preflight validation
	toolsConfig := ToolsConfig{
		Encoder:  "lame",
		LAMEPath: "/nonexistent/lame.exe",
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for invalid lame_path, got nil")
	}
	if !containsString(execErr.Error(), "lame") {
		t.Fatalf("expected error message to mention 'lame', got: %v", execErr)
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

// TestDeleteOnlyPlan_SkipsToolsConfigValidation validates delete-only plans don't require tools config
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

// TestConvertPlan_EmptyEncoder_FailsPreflight validates convert plans fail at preflight if encoder is empty
func TestConvertPlan_EmptyEncoder_FailsPreflight(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Convert plan with EMPTY encoder
	plan := &Plan{
		PlanID: "plan-convert-empty-encoder",
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

	// Empty encoder should fail at preflight (not runtime)
	toolsConfig := ToolsConfig{
		Encoder: "", // Empty encoder
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected error for empty encoder, got nil")
	}
	if !containsString(execErr.Error(), "encoder not configured") {
		t.Fatalf("expected error message to contain 'encoder not configured', got: %v", execErr)
	}
	// Verify source file was NOT deleted (mutation was blocked)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to exist - preflight should fail before any mutation")
	}
}

// TestConvertPlan_TargetExtensionMismatch_FailsPreflight validates convert plan target extension
// must strictly match configured encoder suffix mapping.
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
			TargetPath:             filepath.Join(tmp, "song.m4a"), // mismatch for lame
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "lame",
		LAMEPath: getValidExecutablePath(t),
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected target extension mismatch error, got nil")
	}
	if !containsString(execErr.Error(), "target extension") {
		t.Fatalf("expected error message to contain 'target extension', got: %v", execErr)
	}
	if !containsString(execErr.Error(), ".mp3") {
		t.Fatalf("expected error message to mention required '.mp3', got: %v", execErr)
	}
}

func TestConvertPlan_TargetExtensionMismatch_QAAC_FailsPreflight(t *testing.T) {
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
		PlanID: "plan-target-ext-mismatch-qaac",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.mp3"), // mismatch for qaac
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}

	svc := NewExecuteService(nil, toolsConfig)
	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected target extension mismatch error, got nil")
	}
	if !containsString(execErr.Error(), "target extension") {
		t.Fatalf("expected error message to contain 'target extension', got: %v", execErr)
	}
	if !containsString(execErr.Error(), ".m4a") {
		t.Fatalf("expected error message to mention required '.m4a', got: %v", execErr)
	}
}
