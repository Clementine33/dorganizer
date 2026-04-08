package grpc

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

const determineRootPathBatchChunkSize = 500

type rootPathQueryFunc func(query string, args ...any) (*sql.Rows, error)

func chunkRootResolvePaths(paths []string, chunkSize int) [][]string {
	if chunkSize <= 0 || len(paths) == 0 {
		return nil
	}

	chunks := make([][]string, 0, (len(paths)+chunkSize-1)/chunkSize)
	for start := 0; start < len(paths); start += chunkSize {
		end := start + chunkSize
		if end > len(paths) {
			end = len(paths)
		}
		chunks = append(chunks, paths[start:end])
	}
	return chunks
}

func normalizeUniquePaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		pathPosix := filepath.ToSlash(path)
		if pathPosix == "" {
			continue
		}
		if _, ok := seen[pathPosix]; ok {
			continue
		}
		seen[pathPosix] = struct{}{}
		normalized = append(normalized, pathPosix)
	}
	return normalized
}

func collectDetermineRootCandidates(req *pb.PlanOperationsRequest, plan *analyze.Plan) []string {
	planOpsLen := 0
	if plan != nil {
		planOpsLen = len(plan.Operations)
	}
	candidates := make([]string, 0, planOpsLen+1+len(req.GetFolderPaths())+len(req.GetSourceFiles()))
	if plan != nil {
		for _, op := range plan.Operations {
			if op.SourcePath != "" {
				candidates = append(candidates, op.SourcePath)
			}
		}
	}
	if req.GetFolderPath() != "" {
		candidates = append(candidates, req.GetFolderPath())
	}
	candidates = append(candidates, req.GetFolderPaths()...)
	candidates = append(candidates, req.GetSourceFiles()...)
	return normalizeUniquePaths(candidates)
}

func collectDetermineRootOperationSourceCandidates(req *pb.PlanOperationsRequest, plan *analyze.Plan) []string {
	planOpsLen := 0
	if plan != nil {
		planOpsLen = len(plan.Operations)
	}
	candidates := make([]string, 0, planOpsLen+len(req.GetSourceFiles()))
	if plan != nil {
		for _, op := range plan.Operations {
			if op.SourcePath != "" {
				candidates = append(candidates, op.SourcePath)
			}
		}
	}
	candidates = append(candidates, req.GetSourceFiles()...)
	return normalizeUniquePaths(candidates)
}

func loadRootPathsByExactMatch(queryFn rootPathQueryFunc, paths []string) (map[string]string, error) {
	rootByPath := make(map[string]string, len(paths))
	for _, chunk := range chunkRootResolvePaths(paths, determineRootPathBatchChunkSize) {
		placeholders := slices.Repeat([]string{"?"}, len(chunk))
		args := make([]any, len(chunk))
		for i, path := range chunk {
			args[i] = path
		}

		query := "SELECT path, root_path FROM entries WHERE path IN (" + strings.Join(placeholders, ",") + ")"
		rows, err := queryFn(query, args...)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var path, rootPath string
			if err := rows.Scan(&path, &rootPath); err != nil {
				rows.Close()
				return nil, err
			}
			rootByPath[path] = rootPath
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	return rootByPath, nil
}

func resolveRootPathFromExactMatches(paths []string, batchQuery func(chunk []string) (map[string]string, error)) (string, error) {
	normalizedPaths := normalizeUniquePaths(paths)
	for _, chunk := range chunkRootResolvePaths(normalizedPaths, determineRootPathBatchChunkSize) {
		rootByPath, err := batchQuery(chunk)
		if err != nil {
			return "", err
		}
		for _, path := range chunk {
			if resolved := rootByPath[path]; resolved != "" {
				return resolved, nil
			}
		}
	}
	return "", nil
}

func (s *OnseiServer) determineRootPath(req *pb.PlanOperationsRequest, plan *analyze.Plan, useBatchRootResolve bool) string {
	resolveRootFromEntries := func(path string) (string, error) {
		pathPosix := filepath.ToSlash(path)
		if pathPosix == "" {
			return "", nil
		}
		prefix := strings.TrimSuffix(pathPosix, "/") + "/%"
		var resolved string
		err := s.repo.DB().QueryRow("SELECT root_path FROM entries WHERE path = ? OR path LIKE ? LIMIT 1", pathPosix, prefix).Scan(&resolved)
		if err == sql.ErrNoRows {
			return "", nil
		}
		return resolved, err
	}

	if useBatchRootResolve {
		candidates := collectDetermineRootCandidates(req, plan)
		resolved, err := resolveRootPathFromExactMatches(candidates, func(chunk []string) (map[string]string, error) {
			return loadRootPathsByExactMatch(func(query string, args ...any) (*sql.Rows, error) {
				return s.repo.DB().Query(query, args...)
			}, chunk)
		})
		if err == nil && resolved != "" {
			return resolved
		}
	}

	for _, candidate := range collectDetermineRootOperationSourceCandidates(req, plan) {
		resolved, err := resolveRootFromEntries(candidate)
		if err == nil && resolved != "" {
			return resolved
		}
	}

	if req.GetFolderPath() != "" {
		resolved, err := resolveRootFromEntries(req.GetFolderPath())
		if err == nil && resolved != "" {
			return resolved
		}
	}
	for _, scope := range req.GetFolderPaths() {
		resolved, err := resolveRootFromEntries(scope)
		if err == nil && resolved != "" {
			return resolved
		}
	}

	if req.GetFolderPath() != "" {
		return filepath.ToSlash(filepath.Clean(req.GetFolderPath()))
	}
	if len(req.GetFolderPaths()) > 0 {
		return filepath.ToSlash(filepath.Clean(req.GetFolderPaths()[0]))
	}
	if len(req.GetSourceFiles()) > 0 {
		return filepath.ToSlash(filepath.Clean(filepath.Dir(req.GetSourceFiles()[0])))
	}

	return ""
}

func (s *OnseiServer) computeDeleteTargetPaths(plan *analyze.Plan, rootPath string) {
	if plan == nil || rootPath == "" {
		return
	}

	rootNative := filepath.Clean(filepath.FromSlash(rootPath))
	if !filepath.IsAbs(rootNative) {
		for i := range plan.Operations {
			if plan.Operations[i].Type == analyze.OpTypeDelete {
				plan.Operations[i].TargetPath = ""
			}
		}
		plan.Errors = append(plan.Errors, analyze.PruneError{Path: rootPath, Code: "PATH_ROOT_NOT_ABSOLUTE", Message: fmt.Sprintf("root path must be absolute: %s", rootPath)})
		return
	}

	isUNCRoot := pathnorm.IsWindowsUNCPath(rootNative)
	deleteDir := filepath.ToSlash(filepath.Clean(filepath.Join(rootNative, "Delete")))

	validateBaseName := func(pathValue, src string) bool {
		base := filepath.Base(pathValue)
		if utf8.RuneCountInString(base) > 255 {
			plan.Errors = append(plan.Errors, analyze.PruneError{Path: src, Code: "PATH_NODE_RUNE_TOO_LONG", Message: fmt.Sprintf("path component exceeds 255 runes: %s", base)})
			return false
		}
		if len(base) > 255 {
			plan.Errors = append(plan.Errors, analyze.PruneError{Path: src, Code: "PATH_NODE_BYTE_TOO_LONG", Message: fmt.Sprintf("path component exceeds 255 bytes: %s", base)})
			return false
		}
		return true
	}

	normalizeCandidate := func(candidate, src string) (string, bool) {
		absTarget, err := filepathAbs(filepath.FromSlash(candidate))
		if err != nil {
			plan.Errors = append(plan.Errors, analyze.PruneError{Path: src, Code: "PATH_ABS_FAILED", Message: fmt.Sprintf("failed to get absolute path for delete target: %v", err)})
			return "", false
		}
		canon := filepath.ToSlash(filepath.Clean(absTarget))
		if !strings.HasPrefix(canon, deleteDir+"/") && canon != deleteDir {
			plan.Errors = append(plan.Errors, analyze.PruneError{Path: src, Code: "DELETE_TARGET_ESCAPE_ATTEMPT", Message: fmt.Sprintf("delete target outside Delete directory: %s", canon)})
			return "", false
		}
		if !validateBaseName(canon, src) {
			return "", false
		}
		return canon, true
	}

	for i := range plan.Operations {
		op := &plan.Operations[i]
		if op.Type != analyze.OpTypeDelete {
			continue
		}

		relPath, err := filepath.Rel(rootNative, filepath.FromSlash(op.SourcePath))
		if err != nil {
			plan.Operations[i].TargetPath = ""
			continue
		}

		relPathNorm := filepath.ToSlash(filepath.Clean(relPath))
		if strings.HasPrefix(relPathNorm, "../") || strings.HasPrefix(relPathNorm, "..\\") {
			plan.Operations[i].TargetPath = ""
			plan.Errors = append(plan.Errors, analyze.PruneError{Path: op.SourcePath, Code: "DELETE_TARGET_ESCAPE_ATTEMPT", Message: fmt.Sprintf("delete target escapes root: %s", relPath)})
			continue
		}

		relTargetPath := relPath
		if isUNCRoot {
			relDir := filepath.Dir(relPath)
			if relDir != "." {
				relDir = pathnorm.TruncatePathComponentsToBytes(relDir, 214)
				relTargetPath = filepath.Join(relDir, filepath.Base(relPath))
			}
		}

		baseTarget := filepath.ToSlash(filepath.Join(rootNative, "Delete", relTargetPath))
		canonTarget, ok := normalizeCandidate(baseTarget, op.SourcePath)
		if !ok {
			plan.Operations[i].TargetPath = ""
			continue
		}

		plan.Operations[i].TargetPath = canonTarget
	}
}

func normalizePathForAttribution(root, candidate string) (rootNorm, candidateNorm string, err error) {
	if candidate == "" {
		return "", "", nil
	}

	rootNative := filepath.FromSlash(root)
	rootAbs, err := filepath.Abs(rootNative)
	if err != nil {
		return "", "", err
	}
	rootNorm = filepath.ToSlash(filepath.Clean(rootAbs))

	candidateNative := filepath.FromSlash(candidate)
	candidateAbs, err := filepath.Abs(candidateNative)
	if err != nil {
		return "", "", err
	}
	candidateNorm = filepath.ToSlash(filepath.Clean(candidateAbs))

	return rootNorm, candidateNorm, nil
}

func attributeFolderPath(rootPath, candidatePath string) string {
	rootNorm, candidateNorm, err := normalizePathForAttribution(rootPath, candidatePath)
	if err != nil {
		return ""
	}

	if candidateNorm == "" {
		return ""
	}

	rootNorm = strings.TrimSuffix(rootNorm, "/")
	candidateNorm = strings.TrimSuffix(candidateNorm, "/")

	prefix := rootNorm + "/"
	var relPath string

	var isContained, isSame bool
	if runtime.GOOS == "windows" {
		isContained = strings.HasPrefix(strings.ToLower(candidateNorm), strings.ToLower(prefix))
		isSame = strings.EqualFold(candidateNorm, rootNorm)
	} else {
		isContained = strings.HasPrefix(candidateNorm, prefix)
		isSame = candidateNorm == rootNorm
	}

	if isContained {
		relPath = candidateNorm[len(prefix):]
	} else if isSame {
		return ""
	} else {
		return ""
	}

	if relPath == "" {
		return ""
	}

	parts := strings.Split(relPath, "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}

	rootPrefixLen := len(prefix)
	return candidateNorm[:rootPrefixLen] + parts[0]
}
