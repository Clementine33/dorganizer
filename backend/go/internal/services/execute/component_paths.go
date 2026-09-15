package execute

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// Root-boundary and disk-fact guards of a component run: they keep every
// operation inside the member root and verify the frozen file facts before
// anything is created or changed.

func pathUnsafe(p, message string) *ComponentError {
	return componentError(ComponentStagePrecheck, ComponentCodePathUnsafe, p, message, nil)
}

func fileChanged(p, message string) *ComponentError {
	return componentError(ComponentStagePrecheck, ComponentCodeFileChanged, p, message, nil)
}

// checkPaths enforces the root boundary for every referenced path: lexical
// containment, no symlinked component below the root, and no escape through
// the root itself. Symlinks are refused rather than followed so a link cannot
// redirect an operation outside the member root.
func (p *plannedComponent) checkPaths() *ComponentError {
	seen := make(map[string]bool)
	check := func(raw string) *ComponentError {
		native := nativePath(raw)
		if seen[native] {
			return nil
		}
		seen[native] = true
		return p.checkPath(native)
	}
	for _, enc := range p.encodes {
		if cerr := check(enc.source); cerr != nil {
			return cerr
		}
	}
	for _, enc := range p.encodes {
		if cerr := check(enc.target); cerr != nil {
			return cerr
		}
	}
	for _, rem := range p.removes {
		if cerr := check(rem.source); cerr != nil {
			return cerr
		}
	}
	return nil
}

func (p *plannedComponent) checkPath(native string) *ComponentError {
	if !filepath.IsAbs(native) {
		return pathUnsafe(posixForm(native), "path is not absolute")
	}
	posix := posixForm(native)
	if posix == posixForm(p.absRoot) || !pathnorm.IsWithinRoot(posixForm(p.absRoot), posix) {
		return pathUnsafe(posix, "path escapes the member root")
	}
	rel, err := filepath.Rel(p.absRoot, native)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return pathUnsafe(posix, "path escapes the member root")
	}
	current := p.realRoot
	for component := range strings.SplitSeq(rel, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, fs.ErrNotExist) {
			break
		}
		if statErr != nil {
			return pathUnsafe(posix, fmt.Sprintf("cannot inspect path component %s", posixForm(current)))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return pathUnsafe(posix, fmt.Sprintf("path component %s is a symbolic link", posixForm(current)))
		}
	}
	return nil
}

// checkDiskFacts revalidates every referenced file against the frozen
// (path, size, mtime) facts immediately before any file is created or changed.
// An encode target that already exists among the frozen files counts as an
// in-place replacement only when the plan carries a materialize decision for
// it; anything else is refused instead of silently replaced.
func (p *plannedComponent) checkDiskFacts(c reconcile.ComponentOutcome) *ComponentError {
	frozen := make(map[string]reconcile.FileTuple, len(c.Files))
	for _, tuple := range c.Files {
		frozen[posixForm(nativePath(tuple.Path))] = tuple
	}
	declared := make(map[string]bool)
	for _, variant := range c.Variants {
		for _, decision := range variant.Decisions {
			if decision.Resolution == reconcile.ResolutionEncode && decision.TargetPath != "" {
				declared[posixForm(nativePath(decision.TargetPath))] = true
			}
		}
	}
	for i := range p.encodes {
		enc := &p.encodes[i]
		if cerr := p.checkFrozenFile(frozen, enc.source, "encode source"); cerr != nil {
			return cerr
		}
		if _, listed := frozen[posixForm(enc.target)]; listed {
			if !declared[posixForm(enc.target)] {
				return componentError(ComponentStagePrecheck, ComponentCodeConflict, posixForm(enc.target),
					"existing target is not declared as a replacement output by the frozen plan", nil)
			}
			enc.replace = true
			if cerr := p.checkFrozenFile(frozen, enc.target, "replaced output"); cerr != nil {
				return cerr
			}
			continue
		}
		if cerr := p.checkFreeTarget(enc.target); cerr != nil {
			return cerr
		}
	}
	for _, rem := range p.removes {
		if cerr := p.checkFrozenFile(frozen, rem.source, "obsolete file"); cerr != nil {
			return cerr
		}
	}
	return nil
}

// checkFrozenFile requires an existing regular file whose size and mtime still
// agree with the frozen tuple (the legacy ±1s mtime tolerance).
func (p *plannedComponent) checkFrozenFile(
	frozen map[string]reconcile.FileTuple, native, role string,
) *ComponentError {
	posix := posixForm(native)
	tuple, listed := frozen[posix]
	if !listed {
		return fileChanged(posix, role+" is not part of the frozen component")
	}
	info, err := os.Lstat(native)
	if errors.Is(err, fs.ErrNotExist) {
		return fileChanged(posix, role+" is missing from disk")
	}
	if err != nil {
		return fileChanged(posix, "cannot inspect "+role)
	}
	if !info.Mode().IsRegular() {
		return fileChanged(posix, role+" is not a regular file")
	}
	if tuple.Size > 0 && info.Size() != tuple.Size {
		return fileChanged(posix, fmt.Sprintf("size changed: frozen %d, disk %d", tuple.Size, info.Size()))
	}
	if tuple.Mtime > 0 {
		delta := info.ModTime().Sub(time.Unix(tuple.Mtime, 0))
		if delta < 0 {
			delta = -delta
		}
		if delta > time.Second {
			return fileChanged(
				posix,
				fmt.Sprintf("mtime changed: frozen %d, disk %d", tuple.Mtime, info.ModTime().Unix()),
			)
		}
	}
	if within, err := pathnorm.IsResolvedWithinRoot(posixForm(p.absRoot), posix); err != nil || !within {
		return pathUnsafe(posix, "resolved path escapes the member root")
	}
	return nil
}

// checkFreeTarget requires a fresh target that does not exist yet and sits in
// an existing directory inside the root.
func (p *plannedComponent) checkFreeTarget(native string) *ComponentError {
	posix := posixForm(native)
	if _, err := os.Lstat(native); err == nil {
		return componentError(ComponentStagePrecheck, ComponentCodeConflict, posix,
			"target already exists and is not part of the frozen component", nil)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return componentError(ComponentStagePrecheck, ComponentCodeConflict, posix,
			"cannot inspect target", err)
	}
	parent := filepath.Dir(native)
	info, err := os.Lstat(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return componentError(ComponentStagePrecheck, ComponentCodeConflict, posix,
			"target directory does not exist", nil)
	}
	if err != nil {
		return componentError(ComponentStagePrecheck, ComponentCodeConflict, posix,
			"cannot inspect target directory", err)
	}
	if !info.IsDir() {
		return componentError(ComponentStagePrecheck, ComponentCodeConflict, posix,
			"target directory is not a directory", nil)
	}
	if within, err := pathnorm.IsResolvedWithinRoot(posixForm(p.absRoot), posixForm(parent)); err != nil || !within {
		return pathUnsafe(posix, "target directory resolves outside the member root")
	}
	return nil
}
