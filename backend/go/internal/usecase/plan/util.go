package plan

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/services/analyze"
)

func derivePlanType(targetFormat string) string {
	if strings.HasPrefix(targetFormat, "prune:") {
		return "prune"
	}
	return "slim"
}

func encoderTargetExt(encoder string) string {
	if strings.EqualFold(strings.TrimSpace(encoder), "lame") {
		return ".mp3"
	}
	return ".m4a"
}

func rewriteConvertTargetsToExt(plan *analyze.Plan, targetExt string) {
	if plan == nil {
		return
	}
	for i, op := range plan.Operations {
		if op.Type != analyze.OpTypeConvert || op.TargetPath == "" {
			continue
		}
		dir := filepath.Dir(op.TargetPath)
		base := filepath.Base(op.TargetPath)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		plan.Operations[i].TargetPath = filepath.ToSlash(filepath.Join(dir, stem+targetExt))
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeScopePath(path string) string {
	native := filepath.FromSlash(path)
	cleaned := filepath.Clean(native)
	return filepath.ToSlash(cleaned)
}

func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

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
			if targetFormat != "" && !strings.HasPrefix(targetFormat, "prune:") {
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
	return "plan-" + time.Now().Format("20060102150405")
}

func generateSnapshotToken() string {
	return "snapshot-" + time.Now().Format("20060102150405.000000")
}

func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "sqlite_locked")
}
