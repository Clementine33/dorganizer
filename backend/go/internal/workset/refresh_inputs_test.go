package workset_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
	"github.com/onsei/organizer/backend/internal/workset"
)

// TestGenerationRefreshesInputsBeforePlanning pins the session-time scan: the
// plan reads what the scan just wrote, not what the last scan left behind.
func TestGenerationRefreshesInputsBeforePlanning(t *testing.T) {
	var calls [][2]string
	var f *fixture
	f = newFixtureWithScan(t, func(_ context.Context, folderPath, rootPath string) error {
		calls = append(calls, [2]string{folderPath, rootPath})
		// What a real scan records: the file that appeared since the last one.
		f.insertAudioEntry("/music/albumA/01.mp3", "/music", 1024, 1000)
		return nil
	})
	ids := f.standardLibrary("albumA")
	ws := f.createCurrent("刷新输入", ids...)

	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" {
		t.Fatalf("generation: %+v", gen)
	}
	if len(calls) != 1 || calls[0][0] != "/music/albumA" || calls[0][1] != "/music" {
		t.Fatalf("scan calls = %v, want one scoped scan of the member folder under the library root", calls)
	}
	// The plan saw the entry the scan had just written: a lone mp3 against the
	// wav+mp3 profile is an unmet target, never a missing root.
	rev := f.operation(ws.WorksetID).CurrentRevision
	if rev == nil {
		t.Fatal("expected a current revision")
	}
	if rev.Counts.UnmetTargets != 1 || rev.Counts.Blocked != 0 {
		t.Fatalf("counts = %+v, want one unmet target and no blocked root", rev.Counts)
	}
}

// TestSessionFailsWhenTheRefreshFails covers the other half of the contract:
// planning from unverified facts is exactly what the scan exists to prevent.
func TestSessionFailsWhenTheRefreshFails(t *testing.T) {
	f := newFixtureWithScan(t, func(context.Context, string, string) error {
		return errors.New("scan exploded")
	})
	ids := f.standardLibrary("albumA")
	f.insertAudioEntry("/music/albumA/01.mp3", "/music", 1024, 1000)
	ws := f.createCurrent("扫描失败", ids...)

	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "failed" || gen.ErrorCode != "SCAN_FAILED" {
		t.Fatalf("generation = %+v, want a failed session naming the scan", gen)
	}
}

// TestExecutionRevalidatesInputsAfterRefresh pins the run-time recheck: a
// folder that drifted after planning stops the session before the first write,
// with every file untouched.
func TestExecutionRevalidatesInputsAfterRefresh(t *testing.T) {
	var f *execFixture
	f = newExecFixtureWithScan(t, func(_ context.Context, folderPath, _ string) error {
		// The disk moved since the revision was frozen; the scan records it.
		_, err := f.repo.DB().Exec(
			"UPDATE entries SET mtime = mtime + 500 WHERE path = ?", folderPath+"/00.mp3",
		)
		return err
	})
	f.runDispatcher()
	source, size, mtime := f.writeAudio("albumA", "00.mp3", []byte("obsolete-audio"))
	f.seedRevision("plan-drift", seedComponent{
		id:        "comp-drift",
		member:    "albumA",
		partition: reconcile.PartitionMatched,
		ops:       []reconcile.Operation{deleteOp("comp-drift", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})

	started := f.mustStart("plan-drift", "k-drift")
	done := f.waitTerminal(started.ExecutionID)
	if done.Status != workset.ExecStatusFailed || done.ErrorCode != workset.ExecBlockedInput {
		t.Fatalf(
			"status = %s (%s: %s), want a failed INPUT_CHANGED session",
			done.Status,
			done.ErrorCode,
			done.ErrorMessage,
		)
	}
	if done.CompletedOperations != 0 {
		t.Fatalf("drift must stop the run before any write, completed = %d", done.CompletedOperations)
	}
	if _, err := os.Stat(filepath.Join(f.root, "albumA", "00.mp3")); err != nil {
		t.Fatalf("the drifted file must be left alone: %v", err)
	}
}

// TestExecutionFailsWhenTheRefreshFails: an execution that cannot verify the
// disk does not write to it.
func TestExecutionFailsWhenTheRefreshFails(t *testing.T) {
	f := newExecFixtureWithScan(t, func(context.Context, string, string) error {
		return errors.New("scan exploded")
	})
	f.runDispatcher()
	source, size, mtime := f.writeAudio("albumA", "00.mp3", []byte("obsolete-audio"))
	f.seedRevision("plan-scanfail", seedComponent{
		id:        "comp-scanfail",
		member:    "albumA",
		partition: reconcile.PartitionMatched,
		ops:       []reconcile.Operation{deleteOp("comp-scanfail", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})

	started := f.mustStart("plan-scanfail", "k-scanfail")
	done := f.waitTerminal(started.ExecutionID)
	if done.Status != workset.ExecStatusFailed || done.ErrorCode != "SCAN_FAILED" {
		t.Fatalf("status = %s (%s: %s), want SCAN_FAILED", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if _, err := os.Stat(filepath.Join(f.root, "albumA", "00.mp3")); err != nil {
		t.Fatalf("an unverified run must not touch files: %v", err)
	}
}
