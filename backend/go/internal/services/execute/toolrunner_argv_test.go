package execute

import (
	"errors"
	"os"
	"testing"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
)

// capturedCmd stores the captured command for testing
type capturedCmd struct {
	argv []string
	err  error
}

// toolRunnerWithCapture is a ToolRunner that captures commands instead of executing them
type toolRunnerWithCapture struct {
	toolsConfig  ToolsConfig
	capturedQAAC *capturedCmd
	capturedLAME *capturedCmd
}

func newToolRunnerWithCapture(config ToolsConfig) *toolRunnerWithCapture {
	return &toolRunnerWithCapture{
		toolsConfig:  config,
		capturedQAAC: &capturedCmd{},
		capturedLAME: &capturedCmd{},
	}
}

// Convert implements the conversion by capturing the argv
func (r *toolRunnerWithCapture) Convert(src, dst string) error {
	encoder := r.toolsConfig.Encoder
	switch encoder {
	case "qaac":
		return r.convertWithQAAC(src, dst)
	case "lame":
		return r.convertWithLAME(src, dst)
	default:
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "invalid encoder: must be 'qaac' or 'lame'",
			Err:     errors.New("invalid encoder"),
		}
	}
}

// convertWithQAACCapture captures argv without executing
func (r *toolRunnerWithCapture) convertWithQAAC(src, dst string) error {
	r.capturedQAAC.argv = []string{
		r.toolsConfig.QAACPath,
		"--ignorelength",
		"--no-optimize",
		"-s",
		"-v", "256",
		"-o", dst,
		src,
	}
	return r.capturedQAAC.err
}

// convertWithLAMECapture captures argv without executing
func (r *toolRunnerWithCapture) convertWithLAME(src, dst string) error {
	r.capturedLAME.argv = []string{
		r.toolsConfig.LAMEPath,
		"-b", "320",
		src,
		dst,
	}
	return r.capturedLAME.err
}

func (r *toolRunnerWithCapture) Delete(path string, soft bool) error {
	return os.Remove(path)
}

// TestQAAC_ExactArgv_AssertsCorrectArguments validates qaac command uses exact expected argv
func TestQAAC_ExactArgv_AssertsCorrectArguments(t *testing.T) {
	runner := newToolRunnerWithCapture(ToolsConfig{
		Encoder:  "qaac",
		QAACPath: "/path/to/qaac.exe",
	})

	err := runner.Convert("/input/song.wav", "/output/song.m4a")
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	expectedArgv := []string{
		"/path/to/qaac.exe",
		"--ignorelength",
		"--no-optimize",
		"-s",
		"-v", "256",
		"-o", "/output/song.m4a",
		"/input/song.wav",
	}

	if len(runner.capturedQAAC.argv) != len(expectedArgv) {
		t.Fatalf("qaac argv length mismatch: expected %d, got %d", len(expectedArgv), len(runner.capturedQAAC.argv))
	}

	for i, arg := range expectedArgv {
		if runner.capturedQAAC.argv[i] != arg {
			t.Errorf("qaac argv[%d]: expected %q, got %q", i, arg, runner.capturedQAAC.argv[i])
		}
	}
}

// TestLAME_ExactArgv_AssertsCorrectArguments validates lame command uses exact expected argv
func TestLAME_ExactArgv_AssertsCorrectArguments(t *testing.T) {
	runner := newToolRunnerWithCapture(ToolsConfig{
		Encoder:  "lame",
		LAMEPath: "/path/to/lame.exe",
	})

	err := runner.Convert("/input/song.wav", "/output/song.mp3")
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	expectedArgv := []string{
		"/path/to/lame.exe",
		"-b", "320",
		"/input/song.wav",
		"/output/song.mp3",
	}

	if len(runner.capturedLAME.argv) != len(expectedArgv) {
		t.Fatalf("lame argv length mismatch: expected %d, got %d", len(expectedArgv), len(runner.capturedLAME.argv))
	}

	for i, arg := range expectedArgv {
		if runner.capturedLAME.argv[i] != arg {
			t.Errorf("lame argv[%d]: expected %q, got %q", i, arg, runner.capturedLAME.argv[i])
		}
	}
}
