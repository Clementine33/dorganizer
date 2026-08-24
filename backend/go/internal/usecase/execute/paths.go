package execute

import (
	"path/filepath"
	"runtime"
	"strings"
)

// normalizePathForAttribution normalizes root and candidate for folder attribution.
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

// attributeFolderPath derives the first-level folder path from a candidate path
// within a root path. Returns empty string if attribution cannot be determined.
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
