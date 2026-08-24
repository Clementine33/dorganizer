package plan

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"facette.io/natsort"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

// FilepathAbs is overridable for tests.
var FilepathAbs = filepath.Abs

type folderPlanResult struct {
	folderPath      string
	folderPathCanon string
	rootPath        string
	entries         []analyze.Entry
	operations      []analyze.Operation
	err             *FolderError // usecase FolderError, not proto
}

type orderedFolderResult struct {
	result             folderPlanResult
	attributedFolder   string
	relativeFolderName string
}

func analyzeFoldersWithErrors(repo *sqlite.Repository, folderPaths []string, analyzer *analyze.Analyzer, targetFormat string, purePattern *regexp.Regexp, bitrateBatchUpdate bool) (string, []analyze.Operation, []*FolderError, []string, error) {
	if len(folderPaths) == 0 {
		return "", nil, nil, nil, nil
	}

	results := make(chan folderPlanResult, len(folderPaths))
	for _, fp := range folderPaths {
		go func(folderPath string) {
			result := folderPlanResult{folderPath: folderPath}

			folderPathCanon, pathErr := validateAndNormalizePath(folderPath)
			if pathErr != nil {
				result.err = pathErr
				absPath, _ := filepath.Abs(filepath.FromSlash(folderPath))
				result.folderPathCanon = filepath.ToSlash(filepath.Clean(absPath))
				results <- result
				return
			}
			result.folderPathCanon = folderPathCanon

			rootPath := ""
			var resolved string
			err := repo.DB().QueryRow("SELECT root_path FROM entries WHERE path LIKE ? LIMIT 1", folderPathCanon+"/%").Scan(&resolved)
			if err == nil && resolved != "" {
				rootPath = resolved
			}
			if rootPath == "" {
				rootPath = folderPathCanon
			}
			result.rootPath = rootPath

			folderPathPosix := folderPathCanon
			if !strings.HasSuffix(folderPathPosix, "/") {
				folderPathPosix += "/"
			}

			rows, err := repo.DB().Query("SELECT path, COALESCE(file_size, size, 0), COALESCE(bitrate, 0), COALESCE(format, '') FROM entries WHERE is_dir = 0 AND path LIKE ?", folderPathPosix+"%")
			if err != nil {
				result.err = &FolderError{Code: "DB_QUERY_FAILED", Message: fmt.Sprintf("failed to query entries: %v", err), FolderPath: folderPathCanon}
				results <- result
				return
			}

			var entries []analyze.Entry
			for rows.Next() {
				var e analyze.Entry
				if err := rows.Scan(&e.PathPosix, &e.FileSize, &e.Bitrate, &e.Format); err != nil {
					rows.Close()
					result.err = &FolderError{Code: "DB_SCAN_FAILED", Message: fmt.Sprintf("failed to scan entry: %v", err), FolderPath: folderPathCanon}
					results <- result
					return
				}
				if _, entryErr := validateAndNormalizePath(e.PathPosix); entryErr != nil {
					entryErr.FolderPath = folderPathCanon
					result.err = entryErr
					rows.Close()
					results <- result
					return
				}
				entries = append(entries, e)
			}
			rows.Close()

			result.entries = entries
			if len(entries) > 0 {
				mode := 2
				if strings.EqualFold(targetFormat, "slim:mode1") {
					mode = 1
				}

				var plan analyze.Plan
				if mode == 2 {
					if err := analyzer.EnrichScopedEntriesBitrateWithBatchOption(entries, bitrateBatchUpdate); err != nil {
						result.err = &FolderError{Code: "SLIM_ANALYZE_FAILED", Message: fmt.Sprintf("failed to enrich scoped bitrate: %v", err), FolderPath: folderPathCanon}
						results <- result
						return
					}
					plan = analyze.AnalyzeSlimMode2(entries, purePattern)
				} else {
					plan = analyze.AnalyzeSlimMode1(entries, purePattern)
				}

				if len(plan.Errors) > 0 {
					firstErr := plan.Errors[0]
					result.err = &FolderError{Code: firstErr.Code, Message: firstErr.Message, FolderPath: folderPathCanon}
					results <- result
					return
				}

				result.operations = plan.Operations
			}
			results <- result
		}(fp)
	}

	collected := make([]folderPlanResult, 0, len(folderPaths))
	for range folderPaths {
		collected = append(collected, <-results)
	}

	ordered := make([]orderedFolderResult, 0, len(collected))
	for _, result := range collected {
		attributedFolder := result.folderPathCanon
		if result.rootPath != "" && result.rootPath != result.folderPathCanon {
			if attributed := attributeFolderPath(result.rootPath, result.folderPathCanon); attributed != "" {
				attributedFolder = attributed
			}
		}
		if attributedFolder == "" {
			attributedFolder = result.folderPathCanon
		}

		relativeFolderName := relativeFolderName(result.rootPath, result.folderPathCanon)

		if result.err == nil && len(result.operations) > 1 {
			sortOperationsDFSNatural(result.operations, result.folderPathCanon)
		}

		ordered = append(ordered, orderedFolderResult{
			result:             result,
			attributedFolder:   attributedFolder,
			relativeFolderName: relativeFolderName,
		})
	}

	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]

		if natsort.Compare(left.relativeFolderName, right.relativeFolderName) {
			return true
		}
		if natsort.Compare(right.relativeFolderName, left.relativeFolderName) {
			return false
		}
		if natsort.Compare(left.result.folderPathCanon, right.result.folderPathCanon) {
			return true
		}
		if natsort.Compare(right.result.folderPathCanon, left.result.folderPathCanon) {
			return false
		}
		return left.result.folderPathCanon < right.result.folderPathCanon
	})

	var allOps []analyze.Operation
	var planErrors []*FolderError
	var successfulFolders []string
	var globalRootPath string

	for _, item := range ordered {
		result := item.result
		if globalRootPath == "" && result.rootPath != "" {
			globalRootPath = result.rootPath
		}

		if result.err != nil {
			result.err.FolderPath = item.attributedFolder
			planErrors = append(planErrors, result.err)
			continue
		}

		successfulFolders = append(successfulFolders, item.attributedFolder)
		allOps = append(allOps, result.operations...)
	}

	return globalRootPath, allOps, planErrors, successfulFolders, nil
}

func analyzeFolderWithErrors(repo *sqlite.Repository, folderPath string, analyzer *analyze.Analyzer, targetFormat string, purePattern *regexp.Regexp, bitrateBatchUpdate bool) (string, []analyze.Operation, []*FolderError, []string, error) {
	rootPath, ops, errors, successes, err := analyzeFoldersWithErrors(repo, []string{folderPath}, analyzer, targetFormat, purePattern, bitrateBatchUpdate)
	return rootPath, ops, errors, successes, err
}

func validateAndNormalizePath(path string) (string, *FolderError) {
	if strings.ContainsRune(path, '\x00') {
		return "", &FolderError{Code: "PATH_NULL_BYTE", Message: "path contains null bytes which are invalid"}
	}

	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if len(part) > 255 {
			if utf8.RuneCountInString(part) > 255 {
				return "", &FolderError{Code: "PATH_NODE_RUNE_TOO_LONG", Message: fmt.Sprintf("path component exceeds 255 runes: %s...", part[:50])}
			}
			return "", &FolderError{Code: "PATH_NODE_BYTE_TOO_LONG", Message: fmt.Sprintf("path component exceeds 255 bytes: %s...", part[:50])}
		}
	}

	nativePath := filepath.FromSlash(path)
	absPath, err := FilepathAbs(nativePath)
	if err != nil {
		return "", &FolderError{Code: "PATH_ABS_FAILED", Message: fmt.Sprintf("failed to get absolute path: %v", err)}
	}

	return filepath.ToSlash(filepath.Clean(absPath)), nil
}

func collectEntriesByScopes(repo *sqlite.Repository, folderPaths []string, folderPath string, sourceFiles []string) ([]analyze.Entry, error) {
	seen := make(map[string]struct{})
	entries := make([]analyze.Entry, 0)

	appendUnique := func(e analyze.Entry) {
		if _, ok := seen[e.PathPosix]; ok {
			return
		}
		seen[e.PathPosix] = struct{}{}
		entries = append(entries, e)
	}

	allFolders := make([]string, 0, len(folderPaths)+1)
	for _, fp := range folderPaths {
		if fp != "" {
			allFolders = append(allFolders, normalizeScopePath(fp))
		}
	}
	if folderPath != "" {
		allFolders = append(allFolders, normalizeScopePath(folderPath))
	}

	allSources := make([]string, 0, len(sourceFiles))
	for _, sf := range sourceFiles {
		if sf != "" {
			allSources = append(allSources, normalizeScopePath(sf))
		}
	}

	if len(allFolders) > 0 {
		const chunkSize = 400

		for start := 0; start < len(allFolders); start += chunkSize {
			end := start + chunkSize
			if end > len(allFolders) {
				end = len(allFolders)
			}
			chunk := allFolders[start:end]
			if len(chunk) == 0 {
				continue
			}

			conditions := make([]string, 0, len(chunk)*2)
			args := make([]interface{}, 0, len(chunk)*2)

			for _, folder := range chunk {
				conditions = append(conditions, "path = ?")
				args = append(args, folder)

				likePattern := escapeLikePattern(folder) + "/%"
				if folder == "/" {
					likePattern = "/%"
				}
				conditions = append(conditions, "path LIKE ? ESCAPE '\\'")
				args = append(args, likePattern)
			}

			query := "SELECT path, COALESCE(file_size, size, 0), COALESCE(bitrate, 0), COALESCE(format, '') FROM entries WHERE is_dir = 0 AND (" + strings.Join(conditions, " OR ") + ")"
			rows, err := repo.DB().Query(query, args...)
			if err != nil {
				return nil, fmt.Errorf("folder query failed: %w", err)
			}

			for rows.Next() {
				var e analyze.Entry
				if err := rows.Scan(&e.PathPosix, &e.FileSize, &e.Bitrate, &e.Format); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan folder entry failed: %w", err)
				}
				appendUnique(e)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("folder rows error: %w", err)
			}
			rows.Close()
		}
	}

	if len(allSources) > 0 {
		const chunkSize = 999

		for start := 0; start < len(allSources); start += chunkSize {
			end := start + chunkSize
			if end > len(allSources) {
				end = len(allSources)
			}
			chunk := allSources[start:end]
			if len(chunk) == 0 {
				continue
			}

			placeholders := make([]string, len(chunk))
			args := make([]interface{}, len(chunk))
			for i, src := range chunk {
				placeholders[i] = "?"
				args[i] = src
			}

			query := "SELECT path, COALESCE(file_size, size, 0), COALESCE(bitrate, 0), COALESCE(format, '') FROM entries WHERE is_dir = 0 AND path IN (" + strings.Join(placeholders, ",") + ")"
			rows, err := repo.DB().Query(query, args...)
			if err != nil {
				return nil, fmt.Errorf("source query failed: %w", err)
			}

			for rows.Next() {
				var e analyze.Entry
				if err := rows.Scan(&e.PathPosix, &e.FileSize, &e.Bitrate, &e.Format); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan source entry failed: %w", err)
				}
				appendUnique(e)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("source rows error: %w", err)
			}
			rows.Close()
		}
	}

	return entries, nil
}

func relativeFolderName(rootPath, folderPath string) string {
	if rootPath == "" || folderPath == "" {
		return folderPath
	}

	rootNative := filepath.FromSlash(rootPath)
	folderNative := filepath.FromSlash(folderPath)
	rel, err := filepath.Rel(rootNative, folderNative)
	if err != nil {
		return folderPath
	}

	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return folderPath
	}

	return rel
}

func sortOperationsDFSNatural(ops []analyze.Operation, folderPath string) {
	type sortableOperation struct {
		op      analyze.Operation
		pathKey string
		segs    []string
	}

	sortable := make([]sortableOperation, len(ops))
	for i, op := range ops {
		pathKey := operationSortPathKey(op)
		sortable[i] = sortableOperation{
			op:      op,
			pathKey: pathKey,
			segs:    operationRelativeSegments(pathKey, folderPath),
		}
	}

	sort.SliceStable(sortable, func(i, j int) bool {
		leftPath := sortable[i].pathKey
		rightPath := sortable[j].pathKey

		leftSegs := sortable[i].segs
		rightSegs := sortable[j].segs

		max := len(leftSegs)
		if len(rightSegs) < max {
			max = len(rightSegs)
		}

		for idx := 0; idx < max; idx++ {
			if leftSegs[idx] == rightSegs[idx] {
				continue
			}

			leftIsFile := idx == len(leftSegs)-1
			rightIsFile := idx == len(rightSegs)-1
			if leftIsFile != rightIsFile {
				return leftIsFile
			}

			if natsort.Compare(leftSegs[idx], rightSegs[idx]) {
				return true
			}
			if natsort.Compare(rightSegs[idx], leftSegs[idx]) {
				return false
			}
			return leftSegs[idx] < rightSegs[idx]
		}

		if len(leftSegs) != len(rightSegs) {
			return len(leftSegs) < len(rightSegs)
		}

		if leftPath != rightPath {
			if natsort.Compare(leftPath, rightPath) {
				return true
			}
			if natsort.Compare(rightPath, leftPath) {
				return false
			}
			return leftPath < rightPath
		}

		return false
	})

	for i := range sortable {
		ops[i] = sortable[i].op
	}
}

func operationSortPathKey(op analyze.Operation) string {
	pathKey := op.SourcePath
	if pathKey == "" {
		pathKey = op.TargetPath
	}
	if pathKey == "" {
		return ""
	}

	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(pathKey)))
}

func operationRelativeSegments(pathKey, folderPath string) []string {
	folderNorm := filepath.ToSlash(filepath.Clean(filepath.FromSlash(folderPath)))
	pathNorm := filepath.ToSlash(filepath.Clean(filepath.FromSlash(pathKey)))

	relPath := pathNorm
	if folderNorm != "" {
		rel, err := filepath.Rel(filepath.FromSlash(folderNorm), filepath.FromSlash(pathNorm))
		if err == nil {
			relNorm := filepath.ToSlash(filepath.Clean(rel))
			if relNorm == "." {
				relPath = ""
			} else if relNorm != ".." && !strings.HasPrefix(relNorm, "../") {
				relPath = relNorm
			}
		}
	}

	relPath = strings.Trim(relPath, "/")
	if relPath == "" {
		return []string{}
	}

	return strings.Split(relPath, "/")
}
