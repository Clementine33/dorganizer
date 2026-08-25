package pathnorm

import (
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func NormalizeToPOSIX(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// IsWithinRoot reports whether candidate is root itself or one of its
// descendants. Paths are compared in their persisted POSIX form, with lexical
// cleaning and component boundaries so sibling prefixes and parent traversal
// cannot escape the root. Windows drive and UNC paths compare case-insensitively.
func IsWithinRoot(root, candidate string) bool {
	if root == "" || candidate == "" {
		return false
	}

	normalizedRoot := NormalizeToPOSIX(root)
	windowsPath := IsWindowsUNCPath(root) || isWindowsDrivePath(normalizedRoot) || strings.HasPrefix(normalizedRoot, "//?/")
	rootIsDrive := isWindowsDriveRoot(normalizedRoot)
	normalizedRoot = path.Clean(normalizedRoot)
	if rootIsDrive {
		normalizedRoot += "/"
	}
	normalizedCandidate := NormalizeToPOSIX(candidate)
	candidateIsDrive := isWindowsDriveRoot(normalizedCandidate)
	normalizedCandidate = path.Clean(normalizedCandidate)
	if candidateIsDrive {
		normalizedCandidate += "/"
	}

	if windowsPath {
		normalizedRoot = strings.ToLower(normalizedRoot)
		normalizedCandidate = strings.ToLower(normalizedCandidate)
	}
	if normalizedCandidate == normalizedRoot {
		return true
	}
	if normalizedRoot == "/" {
		return strings.HasPrefix(normalizedCandidate, "/")
	}
	return strings.HasPrefix(normalizedCandidate, strings.TrimSuffix(normalizedRoot, "/")+"/")
}

// IsResolvedWithinRoot reports whether candidate resolves to root or one of its
// descendants on the local filesystem. Both paths must exist. EvalSymlinks
// also resolves Windows junctions, preventing filesystem indirection from
// escaping a lexically valid library root.
func IsResolvedWithinRoot(root, candidate string) (bool, error) {
	if root == "" || candidate == "" {
		return false, nil
	}

	rootPath, err := filepath.Abs(filepath.FromSlash(NormalizeToPOSIX(root)))
	if err != nil {
		return false, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return false, err
	}

	candidatePath, err := filepath.Abs(filepath.FromSlash(NormalizeToPOSIX(candidate)))
	if err != nil {
		return false, err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidatePath)
	if err != nil {
		return false, err
	}

	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil {
		return false, err
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative), nil
}

func isWindowsDrivePath(p string) bool {
	return len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
}

func isWindowsDriveRoot(p string) bool {
	return len(p) == 3 && isWindowsDrivePath(p) && p[2] == '/'
}

func IsWindowsUNCPath(path string) bool {
	normalized := strings.ReplaceAll(path, "/", "\\")
	return strings.HasPrefix(normalized, `\\?\UNC\`) ||
		(strings.HasPrefix(normalized, `\\`) && !strings.HasPrefix(normalized, `\\?\`))
}

func TruncatePathComponentsToBytes(path string, maxBytes int) string {
	if maxBytes <= 0 || path == "" || path == "." {
		return path
	}

	sep := string(filepath.Separator)
	parts := strings.Split(path, sep)
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			continue
		}
		parts[i] = truncateUTF8ToMaxBytes(part, maxBytes)
	}
	return strings.Join(parts, sep)
}

func truncateUTF8ToMaxBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}

	byteCount := 0
	cut := 0
	for _, r := range s {
		runeBytes := utf8.RuneLen(r)
		if byteCount+runeBytes > maxBytes {
			break
		}
		byteCount += runeBytes
		cut += runeBytes
	}

	return s[:cut]
}
