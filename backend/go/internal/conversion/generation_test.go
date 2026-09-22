package conversion_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	appconfig "github.com/onsei/organizer/backend/internal/adapters/settings"
	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/conversion"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/workset"
)

// TestGenerationRecordClosesTheLoop runs the whole mechanism the way a session
// does: one unit is encoded, committed and credentialed through the conversion
// task, its observed facts are synced into the inventory, and the next plan over
// the same root finds the output satisfied.
//
// The fixture is deliberately a case a measured bitrate cannot judge: libopus
// writes VBR, so two seconds of dense noise at a 160k setting land far below
// 160k. Only the record can explain the second plan finding nothing to do —
// the last assertion pins that the observed rate really is below the target.
func TestGenerationRecordClosesTheLoop(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "album")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := filepath.Join(root, "00.wav")
	target := filepath.Join(root, "00.opus")
	runFFmpeg(t, "-f", "lavfi", "-i", "anoisesrc=duration=2:sample_rate=44100:seed=3", "-ac", "2",
		"-c:a", "pcm_s24le", source)
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}

	repo, err := sqlite.NewRepository(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if _, insertErr := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, bitrate, format, content_rev)
		VALUES (?, ?, ?, '00.wav', 0, ?, ?, 0, 'wav', 1)
	`, filepath.ToSlash(source), filepath.ToSlash(root), filepath.ToSlash(root),
		info.Size(), info.ModTime().Unix()); insertErr != nil {
		t.Fatalf("seed entry: %v", insertErr)
	}

	// The declared shape is the wav source plus the encoded target: a profile
	// that declared the encoded lane alone would make the source obsolete.
	profile := reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded: &reconcile.AudioOutputSpec{
			Codec: reconcile.CodecOpus, Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 160},
		},
	}
	policy := reconcile.Policy{
		SchemaVersion: 1, Mode: reconcile.ModeAvailableSources,
		ClassifierTags: []string{"SEなし"}, Matched: profile, Unmatched: profile,
	}
	res := runOneEncodedUnit(t, repo, dir, root, profile, reconcile.ComponentOutcome{
		ComponentID: "cmp",
		Partition:   reconcile.PartitionMatched,
		Status:      reconcile.StatusOK,
		Files: []reconcile.FileTuple{{
			Path: filepath.ToSlash(source), Size: info.Size(), Mtime: info.ModTime().Unix(),
		}},
		Operations: []reconcile.Operation{{
			Kind: reconcile.OpKindEncode, Phase: reconcile.PhaseMaterializeOutputs,
			ComponentID: "cmp", VariantStem: "00",
			SourcePath: filepath.ToSlash(source), TargetPath: filepath.ToSlash(target),
		}},
	})

	// What the record claims, checked against the file it describes.
	if len(res.Generated) != 1 {
		t.Fatalf("generated = %+v, want exactly one record", res.Generated)
	}
	record := res.Generated[0]
	if record.Path != filepath.ToSlash(target) || record.Codec != "opus" ||
		record.Encoder != "libopus" || record.BitrateKbps != 160 || record.Mode != "vbr" {
		t.Fatalf("record = %+v", record)
	}
	assertRecordDescribesFile(t, record, target)

	// The loop closes: the next plan accepts the output on that record.
	plan, err := conversion.Plan(t.Context(), repo, appconfig.NewReader(dir), conversion.Input{
		Policy: policy,
		Roots:  []conversion.RootInput{{Path: filepath.ToSlash(root), Policy: policy}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Summary.OperationCount != 0 || plan.Summary.UnmetTargets != 0 {
		t.Fatalf("regenerated plan = %+v, want nothing left to do", plan.Summary)
	}
	// ...and the rate really could not have done it: the plan probed the file it
	// just found satisfied, and that average is below the target.
	var probed int64
	if scanErr := repo.DB().QueryRow(
		"SELECT COALESCE(bitrate, 0) FROM entries WHERE path = ?", filepath.ToSlash(target),
	).Scan(&probed); scanErr != nil {
		t.Fatalf("read probed bitrate: %v", scanErr)
	}
	if probed == 0 || probed >= 159000 {
		t.Fatalf("probed bitrate = %d, want a VBR average below the 160k target", probed)
	}
}

// runOneEncodedUnit drives one frozen component through the conversion task the
// way a session does — prepare, encode, commit, then the inventory sync that
// carries the unit's observed facts and credentials — and returns its result.
func runOneEncodedUnit(
	t *testing.T,
	repo *sqlite.Repository,
	configDir, root string,
	profile reconcile.DesiredProfile,
	outcome reconcile.ComponentOutcome,
) workset.UnitResult {
	t.Helper()
	unit := workset.ExecutionUnit{
		Index: 0, ID: outcome.ComponentID, RootPath: filepath.ToSlash(root),
		Partition: string(outcome.Partition), Operations: len(outcome.Operations),
		Payload: jsonBytes(t, profile),
	}
	prepared, err := conversion.New(repo, appconfig.NewReader(configDir)).PrepareUnit(t.Context(), workset.UnitRunInput{
		WorksetRoot: filepath.ToSlash(root),
		Options:     json.RawMessage(`{"delete_mode":"soft"}`),
		Unit:        unit,
		Outcome:     jsonBytes(t, outcome),
	})
	if err != nil {
		t.Fatalf("prepare unit: %v", err)
	}
	for i := range prepared.EncodeTasks() {
		if encodeErr := prepared.EncodeTask(t.Context(), i); encodeErr != nil {
			t.Fatalf("encode %d: %v", i, encodeErr)
		}
	}
	res, err := prepared.Commit(t.Context())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if syncErr := repo.SyncObservedInventory(
		filepath.ToSlash(root), res.InventoryRemoved, res.InventoryRefreshed, res.Generated,
	); syncErr != nil {
		t.Fatalf("sync inventory: %v", syncErr)
	}
	return res
}

// assertRecordDescribesFile checks the binding half of a credential: the size
// and mtime a plan compares against the inventory, and the hash it could compare
// against the bytes.
func assertRecordDescribesFile(t *testing.T, record inventory.GenerationRecord, path string) {
	t.Helper()
	written, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat committed output: %v", err)
	}
	if record.Size != written.Size() || record.Mtime != written.ModTime().Unix() {
		t.Fatalf(
			"record does not describe the file: %+v vs %d/%d",
			record, written.Size(), written.ModTime().Unix(),
		)
	}
	if len(record.ContentSHA256) != 64 {
		t.Fatalf("content hash = %q, want a sha256 hex digest", record.ContentSHA256)
	}
}

// jsonBytes marshals one frozen payload for the task seam.
func jsonBytes(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// runFFmpeg runs one ffmpeg command from this test's own fixtures.
func runFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	//nolint:gosec // The tool path and every argument come from this test's own fixtures.
	cmd := exec.CommandContext(t.Context(), "ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, output)
	}
}
