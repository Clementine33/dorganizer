package plan

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/services/analyze"
)

// normalizeScopePath cleans a scope path to canonical POSIX form.
func normalizeScopePath(path string) string {
	native := filepath.FromSlash(path)
	cleaned := filepath.Clean(native)
	return filepath.ToSlash(cleaned)
}

// escapeLikePattern escapes SQL LIKE wildcards so a user-supplied path can
// never widen a LIKE scope.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// buildSingleFileOperations builds explicit single delete/convert operations.
func buildSingleFileOperations(sourceFiles []string, targetFormat, planType string) ([]analyze.Operation, error) {
	var ops []analyze.Operation

	for _, sourceFile := range sourceFiles {
		sourcePathPosix := filepath.ToSlash(sourceFile)

		var opType analyze.OpType
		var targetPath string

		if planType == "single_delete" {
			opType = analyze.OpTypeDelete
		} else {
			targetExt := ".m4a"
			if targetFormat != "" {
				targetExt = "." + strings.TrimPrefix(targetFormat, ".")
			}
			dir := filepath.Dir(sourceFile)
			stem := strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
			targetPath = filepath.ToSlash(filepath.Join(dir, stem+targetExt))
			opType = analyze.OpTypeConvert
		}

		ops = append(ops, analyze.Operation{Type: opType, SourcePath: sourcePathPosix, TargetPath: targetPath, Reason: "SINGLE_" + strings.ToUpper(planType)})
	}

	return ops, nil
}

func generatePlanID() string {
	// Nanosecond resolution keeps the plans PK collision-free even for
	// immediate successive Plan calls within the same second, while
	// preserving the sortable "plan-<timestamp>" format.
	return "plan-" + time.Now().Format("20060102150405.000000000")
}

func generateSnapshotToken() string {
	return "snapshot-" + time.Now().Format("20060102150405.000000")
}
