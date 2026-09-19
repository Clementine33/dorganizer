package fileops

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// MemberScan refreshes one directory subtree in the stored inventory. It is
// the same disk → inventory seam the planning sessions use, handed in so the
// service does not depend on the scanner.
type MemberScan func(ctx context.Context, folderPath, rootPath string) error

// Service applies direct file-management requests inside a library member.
type Service struct {
	gate *Gate
	scan MemberScan
}

// NewService creates the file-management service. gate is the process-wide
// admission control; scan refreshes the affected inventory after a write (nil
// means the writes are reported without a refresh, which the tests use).
func NewService(gate *Gate, scan MemberScan) *Service {
	return &Service{gate: gate, scan: scan}
}

// Apply runs one request: admission first, then the items in order, then the
// refresh of everything the writes touched.
//
// The whole request — including its refresh — runs inside one direct-file-
// management slot, so the refresh is part of the same protected operation and
// never re-enters admission against itself (spec C1).
//
// Execution stops at the first failure: the remaining items are reported as
// not attempted, and nothing already done is undone. A failed refresh is
// reported as a refresh failure, never as a failed write (spec F2, F4).
func (s *Service) Apply(ctx context.Context, req Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch req.Operation {
	case OpRename, OpMove, OpSoftDelete:
	default:
		return nil, &PathError{Code: CodeOperationDenied, Message: "unsupported operation " + req.Operation}
	}
	if s.gate == nil {
		return nil, errors.New("file operations are not wired to admission control")
	}
	release, err := s.gate.BeginManual()
	if err != nil {
		return nil, err
	}
	defer release()

	m, err := openMember(req.LibraryRoot, req.MemberPath)
	if err != nil {
		return nil, err
	}
	if len(req.Items) == 0 {
		return nil, &PathError{CodePathInvalid, "the request has no items"}
	}

	worklist := planItems(req.Items)
	result := &Result{
		Operation:  req.Operation,
		MemberPath: m.rel,
		Items:      make([]ItemResult, 0, len(worklist)),
	}

	var recycled []string
	stopped := false
	for _, planned := range worklist {
		switch {
		case planned.skip != nil:
			message := "covered by the selected parent directory"
			if *planned.skip == CodeDuplicateItem {
				message = "named more than once"
			}
			result.Items = append(result.Items, ItemResult{
				Source: planned.item.Source, Status: StatusSkipped, Code: *planned.skip,
				Message: message,
			})
		case stopped:
			result.Items = append(result.Items, ItemResult{
				Source: planned.item.Source, Status: StatusNotAttempted,
				Message: "not attempted: an earlier item failed",
			})
		default:
			itemResult := applyOperation(m, req.Operation, planned.item, &recycled)
			result.Items = append(result.Items, itemResult)
			if itemResult.Status == StatusFailed {
				stopped = true
			}
		}
	}

	for _, item := range result.Items {
		switch item.Status {
		case StatusOK:
			result.Succeeded++
		case StatusFailed:
			result.Failed++
		default:
			result.Untouched++
		}
	}
	result.Refresh = s.refreshAfter(ctx, m, recycled)
	return result, nil
}

// planned pairs one requested item with its decision: applied, or skipped
// because a selected ancestor covers it.
type planned struct {
	item Item
	skip *string
}

// planItems keeps the request order and drops the items a selected ancestor
// already covers — a directory and its own child are one operation, and
// deleting the parent first would otherwise leave the child "missing" — as
// well as an item named twice, which is the same operation asked for twice
// (spec F2).
func planItems(items []Item) []planned {
	cleaned := make([]string, len(items))
	for i, item := range items {
		if rel, ok := pathnorm.RelPath(item.Source); ok {
			cleaned[i] = rel
		} else {
			cleaned[i] = item.Source
		}
	}
	out := make([]planned, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		source := cleaned[i]
		var reason *string
		if _, duplicate := seen[source]; duplicate {
			code := CodeDuplicateItem
			reason = &code
			out = append(out, planned{item: item, skip: reason})
			continue
		}
		for j, ancestor := range cleaned {
			if i == j {
				continue
			}
			if isDescendant(source, ancestor) {
				code := CodeCoveredByParent
				reason = &code
				break
			}
		}
		if reason == nil {
			seen[source] = struct{}{}
		}
		out = append(out, planned{item: item, skip: reason})
	}
	return out
}

// isDescendant reports whether candidate is strictly below ancestor.
func isDescendant(candidate, ancestor string) bool {
	if ancestor == "" || candidate == ancestor {
		return false
	}
	return strings.HasPrefix(candidate, strings.TrimSuffix(ancestor, "/")+"/")
}

// refreshAfter re-reads the inventory of everything the writes touched: the
// member itself, and for a soft delete the recovery directory that received
// the media. The refresh belongs to the same admitted operation, so it does
// not ask for admission again.
func (s *Service) refreshAfter(ctx context.Context, m *member, recycled []string) RefreshState {
	if s.scan == nil {
		return RefreshState{OK: true}
	}
	scopes := []string{m.nativeAbs}
	scopes = append(scopes, recoveryRoots(m, recycled)...)
	for _, scope := range scopes {
		if err := s.scan(ctx, filepath.FromSlash(scope), m.nativeRoot); err != nil {
			return RefreshState{
				OK:      false,
				Code:    "REFRESH_FAILED",
				Message: "files were modified, but refreshing the inventory failed: " + err.Error(),
			}
		}
	}
	return RefreshState{OK: true}
}

// recoveryRoots is the set of library-root-level directories that received
// recycled media, deepest-last so each is refreshed once.
func recoveryRoots(m *member, recycled []string) []string {
	seen := map[string]struct{}{}
	for _, recovered := range recycled {
		rel, err := filepath.Rel(m.nativeRoot, recovered)
		if err != nil {
			continue
		}
		relDir := filepath.ToSlash(filepath.Dir(rel))
		// Refresh the top of the recovery tree rather than each leaf directory:
		// one scan covers them all.
		parts := strings.Split(relDir, "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		top := parts[0]
		abs := path.Join(pathnorm.NormalizeToPOSIX(m.root), top)
		seen[abs] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for abs := range seen {
		out = append(out, abs)
	}
	sort.Strings(out)
	return out
}

// RecoveryDir is the library-root-level directory that receives recycled
// media. It is not a member and never a browsing scope.
const RecoveryDir = pathnorm.RecoveryDirName

// ResolveMember validates a library-relative member path for reading (no
// admission, no write): it must be a direct child directory of the library
// root and must exist as a real directory. Reading a member tree and writing
// inside one resolve the member through this same function, so both refuse the
// same paths.
func ResolveMember(rootPath, memberRel string) (string, error) {
	m, err := openMember(rootPath, memberRel)
	if err != nil {
		return "", err
	}
	return m.nativeAbs, nil
}
