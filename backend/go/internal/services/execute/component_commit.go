package execute

import (
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
)

// tempOutputPath returns this run's unique staging path beside its target, so
// the commit rename stays on one filesystem and a concurrent run cannot share
// the file.
func tempOutputPath(target string) string {
	return filepath.Join(filepath.Dir(target), filepath.Base(target)+".tmp."+uuid.NewString()[:8])
}

// replacedAsidePath returns the staging name that holds a replaced old output
// until the new output has landed (hard mode only).
func replacedAsidePath(target string) string {
	return filepath.Join(filepath.Dir(target), filepath.Base(target)+".tmp-old."+uuid.NewString()[:8])
}

// commitOutput moves one validated temporary output onto its target. A fresh
// target is renamed into place directly. A same-path replacement first keeps
// the old file recoverable: soft mode moves it under Delete/ before the new
// output lands; hard mode renames it aside until the new output has landed and
// removes the copy afterwards. The boolean reports whether the new output
// landed — a cleanup failure after a landing output still reports true — and
// the returned recovery paths (persisted form) still hold old media whenever
// replacement or cleanup did not fully complete.
func commitOutput(tk *componentToolkit, absRoot string, enc *plannedEncode, soft bool) ([]string, bool, error) {
	target := posixForm(enc.target)
	if !enc.replace {
		if err := tk.rename(enc.temp, enc.target); err != nil {
			return nil, false, fmt.Errorf("commit %s: %w", target, err)
		}
		return nil, true, nil
	}
	if soft {
		recoveryDest, err := softRemove(tk, absRoot, enc.target)
		if err != nil {
			return nil, false, fmt.Errorf("protect replaced output %s: %w", target, err)
		}
		if err := tk.rename(enc.temp, enc.target); err != nil {
			restoreErr := tk.rename(recoveryDest, enc.target)
			if restoreErr != nil {
				return []string{posixForm(recoveryDest)}, false, fmt.Errorf(
					"commit %s: %w (old file kept at %s; restoring it failed: %w)",
					target, err, posixForm(recoveryDest), restoreErr)
			}
			return nil, false, fmt.Errorf("commit %s: %w", target, err)
		}
		return []string{posixForm(recoveryDest)}, true, nil
	}
	aside := replacedAsidePath(enc.target)
	if err := tk.rename(enc.target, aside); err != nil {
		return nil, false, fmt.Errorf("protect replaced output %s: %w", target, err)
	}
	if err := tk.rename(enc.temp, enc.target); err != nil {
		restoreErr := tk.rename(aside, enc.target)
		if restoreErr != nil {
			return []string{posixForm(aside)}, false, fmt.Errorf(
				"commit %s: %w (old file kept at %s; restoring it failed: %w)",
				target, err, posixForm(aside), restoreErr)
		}
		return nil, false, fmt.Errorf("commit %s: %w", target, err)
	}
	if err := tk.remove(aside); err != nil {
		return []string{posixForm(aside)}, true, fmt.Errorf("remove replaced old output %s: %w", posixForm(aside), err)
	}
	return nil, true, nil
}
