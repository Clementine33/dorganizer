package workset

import (
	"context"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// FolderScan refreshes one folder subtree's stored inventory from disk. It is
// the seam a session uses to plan and to execute against what is on disk now
// rather than against whatever the last scan left behind (ADR 0001 §2).
//
// rootPath is the owning library's root: entries are keyed by it, so a scoped
// scan must not rewrite that key with the folder it walked.
type FolderScan func(ctx context.Context, folderPath, rootPath string) error

// refreshRoots scans every participating folder before a session reads or
// writes the filesystem. A scan failure is fatal to the session: planning from
// unverified facts is exactly what this exists to prevent. onRoot runs between
// folders so a session can observe its cooperative cancel flag.
func (s *serviceImpl) refreshRoots(
	ctx context.Context,
	folders []string,
	rootPath string,
	onRoot func(),
) error {
	if s.scanFolder == nil {
		return nil
	}
	for _, folder := range folders {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.scanFolder(ctx, folder, rootPath); err != nil {
			return err
		}
		if onRoot != nil {
			onRoot()
		}
	}
	return ctx.Err()
}

// participatingRoots lists the folders a planning session will cover: the
// members that take part in the frozen draft, in workset order.
func (s *serviceImpl) participatingRoots(
	operationType string,
	draft []byte,
	members []*sqlite.WorksetMember,
) ([]string, error) {
	task, err := s.requireTask(operationType)
	if err != nil {
		return nil, err
	}
	facts, err := task.RevisionMembers(RevisionFacts{DraftSnapshot: draft, Members: members})
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(facts))
	for _, f := range facts {
		if !f.Excluded {
			roots = append(roots, f.FolderPath)
		}
	}
	return roots, nil
}

// unitRoots lists the distinct roots a frozen worklist touches, in order.
func unitRoots(units []ExecutionUnit) []string {
	seen := make(map[string]bool, len(units))
	roots := make([]string, 0, len(units))
	for _, u := range units {
		if u.RootPath == "" || seen[u.RootPath] {
			continue
		}
		seen[u.RootPath] = true
		roots = append(roots, u.RootPath)
	}
	return roots
}
