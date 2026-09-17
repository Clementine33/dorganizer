package workset_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// execFixture drives the execution session API against a real repository and a
// real filesystem: members are real directories holding real files, and the
// frozen revision is seeded with the component outcomes the worker consumes.
type execFixture struct {
	t         *testing.T
	repo      *sqlite.Repository
	svc       worksetusecase.Service
	root      string
	libraryID string
	worksetID string
	members   map[string]worksetusecase.MemberView // folder name -> member
	draftJSON string
	draftHash string
}

func newExecFixture(t *testing.T) *execFixture {
	t.Helper()
	return newExecFixtureWithScan(t, nil)
}

// newExecFixtureWithScan wires the session-time folder refresh seam; a nil scan
// keeps the stored inventory as the only input facts.
func newExecFixtureWithScan(t *testing.T, scan worksetusecase.FolderScan) *execFixture {
	t.Helper()
	return newExecFixtureWithTask(t, scan, 1, nil)
}

// newExecFixtureWithTask wires the fixture with the given registered tasks and
// execution encode concurrency; a nil tasks list registers the real conversion
// task.
func newExecFixtureWithTask(
	t *testing.T,
	scan worksetusecase.FolderScan,
	encodeConcurrency int,
	tasks []worksetusecase.Task,
) *execFixture {
	t.Helper()
	tmp := t.TempDir()
	repo, err := sqlite.NewRepository(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	cfg := `{"prune":{"literal_tags":["SEなし"]}}`
	if cfgErr := os.WriteFile(filepath.Join(tmp, "config.json"), []byte(cfg), 0o644); cfgErr != nil {
		t.Fatalf("write config: %v", cfgErr)
	}
	if tasks == nil {
		tasks = []worksetusecase.Task{tasksconversion.New(tmp)}
	}
	root := filepath.Join(tmp, "music")
	f := &execFixture{
		t:         t,
		repo:      repo,
		svc:       worksetusecase.NewService(repo, 1, encodeConcurrency, tasks, scan),
		root:      root,
		libraryID: "lib-1",
		members:   map[string]worksetusecase.MemberView{},
	}
	if mkdirErr := os.MkdirAll(root, 0o755); mkdirErr != nil {
		t.Fatalf("mkdir root: %v", mkdirErr)
	}
	now := time.Now().Format(timeFmt)
	if _, insertErr := repo.DB().Exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES (?, 'Onsei', ?, ?, ?, ?)
	`, f.libraryID, root, pathnorm.RootPathKey(root), now, now); insertErr != nil {
		t.Fatalf("insert library: %v", insertErr)
	}
	folderIDs := make([]string, 0, 2)
	for _, name := range []string{"albumA", "albumB"} {
		dir := filepath.Join(root, name)
		if dirErr := os.MkdirAll(dir, 0o755); dirErr != nil {
			t.Fatalf("mkdir %s: %v", dir, dirErr)
		}
		id := "f-" + name
		if _, folderErr := repo.DB().Exec(`
			INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 0, ?, ?)
		`, id, f.libraryID, dir, name, name, now, now); folderErr != nil {
			t.Fatalf("insert folder: %v", folderErr)
		}
		folderIDs = append(folderIDs, id)
	}
	res, err := f.svc.CreateWorkset(context.Background(), worksetusecase.CreateRequest{
		LibraryID: f.libraryID, Title: "実行", FolderIDs: folderIDs,
	})
	if err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	f.worksetID = res.Workset.WorksetID
	for _, m := range res.Workset.Members {
		f.members[m.FolderName] = m
	}
	draft := f.draft()
	doc, err := tasksconversion.ParseDraft(string(draft.Document))
	if err != nil {
		t.Fatalf("parse draft: %v", err)
	}
	raw, hash, err := tasksconversion.MarshalDraft(doc)
	if err != nil {
		t.Fatalf("marshal draft: %v", err)
	}
	f.draftJSON, f.draftHash = raw, hash
	return f
}

func (f *execFixture) draft() *worksetusecase.Draft {
	f.t.Helper()
	d, err := f.svc.GetDraft(f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		f.t.Fatalf("GetDraft: %v", err)
	}
	return d
}

func (f *execFixture) operation() *worksetusecase.OperationView {
	f.t.Helper()
	view, err := f.svc.GetOperation(f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		f.t.Fatalf("GetOperation: %v", err)
	}
	return view
}

// writeAudio writes a real file and records its scan row.
func (f *execFixture) writeAudio(member, name string, content []byte) (string, int64, int64) {
	f.t.Helper()
	dir := filepath.Join(f.root, member)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		f.t.Fatalf("write %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		f.t.Fatalf("stat %s: %v", path, err)
	}
	now := time.Now().Format(timeFmt)
	format := strings.TrimPrefix(filepath.Ext(name), ".")
	if ext := filepath.Ext(name); ext == ".mp3" {
		format = "mpeg"
	}
	if _, err := f.repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format, content_rev, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?, 1, ?)
	`, filepath.ToSlash(path), filepath.ToSlash(f.root), filepath.ToSlash(dir), name,
		info.Size(), info.ModTime().Unix(), format, now); err != nil {
		f.t.Fatalf("insert entry: %v", err)
	}
	return filepath.ToSlash(path), info.Size(), info.ModTime().Unix()
}

// liveFingerprint recomputes a root's inventory fingerprint from the entries
// table with the planner's own collection rules.
func (f *execFixture) liveFingerprint(root string) (string, int) {
	f.t.Helper()
	rows, err := f.repo.DB().Query(`
		SELECT path, COALESCE(size, 0), COALESCE(mtime, 0), COALESCE(bitrate, 0), COALESCE(format, '')
		FROM entries WHERE is_dir = 0 AND (path = ? OR path LIKE ? ESCAPE '\')
	`, filepath.ToSlash(root), filepath.ToSlash(root)+"/%")
	if err != nil {
		f.t.Fatalf("collect entries: %v", err)
	}
	defer rows.Close()
	var entries []reconcile.AudioEntry
	for rows.Next() {
		var e reconcile.AudioEntry
		if err := rows.Scan(&e.PathPosix, &e.Size, &e.Mtime, &e.Bitrate, &e.Format); err != nil {
			f.t.Fatalf("scan entry: %v", err)
		}
		entries = append(entries, e)
	}
	digest, count := reconcile.InventoryFingerprint(reconcile.AudioEntries(entries))
	return digest, count
}

// seedUnpromotedRevision writes a plan and its revision association without
// promoting it: the operation's current revision stays whatever it was.
func (f *execFixture) seedUnpromotedRevision(planID string) {
	f.t.Helper()
	if err := sqlite.CreatePlanTx(
		f.repo.DB(), planID, "conversion", 1, filepath.ToSlash(f.root), "snap-"+planID, f.libraryID,
		nil, nil, nil,
	); err != nil {
		f.t.Fatalf("CreatePlanTx: %v", err)
	}
	now := time.Now().Format(timeFmt)
	if _, err := f.repo.DB().Exec(`
		INSERT INTO workset_operation_revisions
			(plan_id, workset_id, operation_type, revision_index, draft_hash, member_hash, operation_version, excluded_scope, draft_snapshot, created_at)
		VALUES (?, ?, 'conversion', 1, ?, 'members', 0, '', ?, ?)
	`, planID, f.worksetID, f.draftHash, f.draftJSON, now); err != nil {
		f.t.Fatalf("insert revision: %v", err)
	}
}

// seedComponent describes one frozen component of the seeded revision.
type seedComponent struct {
	id        string
	member    string
	partition reconcile.Partition
	ops       []reconcile.Operation
	files     []reconcile.FileTuple
	variants  []reconcile.VariantDecision
}

// seedRevision writes a plan snapshot, the operation revision association and
// the promotion.
func (f *execFixture) seedRevision(planID string, comps ...seedComponent) {
	f.t.Helper()
	rootIndex := map[string]int{}
	roots := make([]sqlite.PlanRootRecord, 0, len(comps))
	var compsRecords []sqlite.PlanComponentRecord
	for i, c := range comps {
		member, ok := f.members[c.member]
		if !ok {
			f.t.Fatalf("unknown member %q", c.member)
		}
		idx, seen := rootIndex[member.FolderPath]
		if !seen {
			idx = len(roots)
			rootIndex[member.FolderPath] = idx
			digest, count := f.liveFingerprint(member.FolderPath)
			roots = append(roots, sqlite.PlanRootRecord{
				RootIndex:            idx,
				RootPath:             member.FolderPath,
				InventoryFingerprint: digest,
				EntryCount:           count,
				RootStatus:           "ok",
			})
		}
		compID := c.id
		if compID == "" {
			compID = "comp-" + planID + "-" + strconv.Itoa(i)
		}
		outcome := reconcile.ComponentOutcome{
			ComponentID: compID,
			Partition:   c.partition,
			Status:      reconcile.StatusOK,
			Lanes:       []reconcile.LaneDecision{},
			Variants:    c.variants,
			Operations:  c.ops,
			Files:       c.files,
		}
		raw, err := json.Marshal(outcome)
		if err != nil {
			f.t.Fatalf("marshal outcome: %v", err)
		}
		compsRecords = append(compsRecords, sqlite.PlanComponentRecord{
			StepIndex:      0,
			ComponentIndex: i,
			ComponentID:    outcome.ComponentID,
			RootIndex:      idx,
			Partition:      string(c.partition),
			Status:         reconcile.StatusOK,
			OutcomeJSON:    string(raw),
		})
	}
	steps := []sqlite.PlanStepRecord{{
		StepIndex:       0,
		StepType:        "reconcile_audio",
		Status:          "ok",
		StepSummaryJSON: `{"component_count":` + strconv.Itoa(len(comps)) + `,"summary_reason":"ACTIONABLE"}`,
	}}
	if err := sqlite.CreatePlanTx(
		f.repo.DB(), planID, "conversion", 1, filepath.ToSlash(f.root), "snap-"+planID, f.libraryID,
		steps, roots, compsRecords,
	); err != nil {
		f.t.Fatalf("CreatePlanTx: %v", err)
	}
	now := time.Now().Format(timeFmt)
	if _, err := f.repo.DB().Exec(`
		INSERT INTO workset_operation_revisions
			(plan_id, workset_id, operation_type, revision_index, draft_hash, member_hash, operation_version, excluded_scope, draft_snapshot, created_at)
		VALUES (?, ?, 'conversion', 1, ?, 'members', 0, '', ?, ?)
	`, planID, f.worksetID, f.draftHash, f.draftJSON, now); err != nil {
		f.t.Fatalf("insert revision: %v", err)
	}
	if _, err := f.repo.DB().Exec(`
		UPDATE workset_operations SET current_revision_id = ?, version = version + 1, updated_at = ?
		WHERE workset_id = ? AND operation_type = 'conversion'
	`, planID, now, f.worksetID); err != nil {
		f.t.Fatalf("promote revision: %v", err)
	}
}

// setDraftDeleteMode re-freezes the fixture draft with an explicit
// obsolete-audio handling, so a seeded revision carries it (the session
// options come from the revision's own draft snapshot). The stored draft is
// updated too, exactly as a real draft save would leave it.
func (f *execFixture) setDraftDeleteMode(mode string) {
	f.t.Helper()
	doc, err := tasksconversion.ParseDraft(f.draftJSON)
	if err != nil {
		f.t.Fatalf("parse draft: %v", err)
	}
	doc.DeleteMode = mode
	raw, hash, err := tasksconversion.MarshalDraft(doc)
	if err != nil {
		f.t.Fatalf("marshal draft: %v", err)
	}
	f.draftJSON, f.draftHash = raw, hash
	if _, err := f.repo.DB().Exec(
		`UPDATE workset_operation_drafts SET draft_json = ?, draft_hash = ?
		 WHERE workset_id = ? AND operation_type = 'conversion'`,
		raw, hash, f.worksetID,
	); err != nil {
		f.t.Fatalf("update stored draft: %v", err)
	}
}

// sessionDeleteMode reads the frozen session option back out of the view.
func sessionDeleteMode(t *testing.T, options json.RawMessage) string {
	t.Helper()
	var opts struct {
		DeleteMode string `json:"delete_mode"`
	}
	if err := json.Unmarshal(options, &opts); err != nil {
		t.Fatalf("parse session options: %v", err)
	}
	return opts.DeleteMode
}

// startExecution enqueues a session through the exported API.
func (f *execFixture) startExecution(
	planID, key string,
) (*worksetusecase.StartExecutionResult, error) {
	f.t.Helper()
	return f.svc.StartExecution(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, planID,
		worksetusecase.StartExecutionRequest{
			IfMatchVersion: f.operation().Version,
			IdempotencyKey: key,
		},
	)
}

func (f *execFixture) mustStart(planID, key string) *worksetusecase.ExecutionView {
	f.t.Helper()
	res, err := f.startExecution(planID, key)
	if err != nil {
		f.t.Fatalf("StartExecution: %v", err)
	}
	return res.Execution
}

// runDispatcher starts the worker pool for the test and stops it afterwards.
func (f *execFixture) runDispatcher() {
	f.t.Helper()
	f.svc.DispatcherHandle().Start()
	f.t.Cleanup(f.svc.DispatcherHandle().Stop)
}

// waitTerminal polls the session until it reaches a terminal status.
func (f *execFixture) waitTerminal(executionID string) *worksetusecase.ExecutionView {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		view, err := f.svc.GetExecution(
			f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, executionID,
		)
		if err != nil {
			f.t.Fatalf("GetExecution: %v", err)
		}
		switch view.Status {
		case sqlite.ExecStatusSucceeded, sqlite.ExecStatusFailed,
			sqlite.ExecStatusCanceled, sqlite.ExecStatusInterrupted:
			return view
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("execution %s did not finish in time", executionID)
	return nil
}

// contains reports whether a reason detail is present.
func contains(details []string, want string) bool {
	return slices.Contains(details, want)
}

// errorDetail extracts the error code and reason details of a failed call.
func errorDetail(t *testing.T, err error) (string, []string) {
	t.Helper()
	werr, ok := worksetusecase.AsError(err)
	if !ok {
		t.Fatalf("error is not a workset error: %v", err)
	}
	return werr.Code, werr.Details
}

func deleteOp(componentID, path string) reconcile.Operation {
	return reconcile.Operation{
		Kind:        reconcile.OpKindRemoveObsolete,
		Phase:       reconcile.PhaseRemoveObsoleteAudio,
		ComponentID: componentID,
		SourcePath:  path,
	}
}

func TestStartExecutionGates(t *testing.T) {
	t.Run("not_current_revision", func(t *testing.T) {
		f := newExecFixture(t)
		// A revision row that was never promoted to the operation's current one.
		f.seedUnpromotedRevision("plan-old")
		_, err := f.startExecution("plan-old", "k-old")
		code, details := errorDetail(t, err)
		if code != "PLAN_NOT_EXECUTABLE" || !contains(details, "NOT_CURRENT_REVISION") {
			t.Fatalf("err = %s %v, want NOT_CURRENT_REVISION", code, details)
		}
	})

	t.Run("version_conflict", func(t *testing.T) {
		f := newExecFixture(t)
		f.seedRevision("plan-v")
		_, err := f.svc.StartExecution(
			f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, "plan-v",
			worksetusecase.StartExecutionRequest{IfMatchVersion: 1, IdempotencyKey: "k-v"},
		)
		code, _ := errorDetail(t, err)
		if code != "VERSION_CONFLICT" {
			t.Fatalf("err code = %s, want VERSION_CONFLICT", code)
		}
	})

	t.Run("draft_changed", func(t *testing.T) {
		f := newExecFixture(t)
		f.seedRevision("plan-dc")
		doc := mustDraft(t, f.draft())
		doc.Mode = reconcile.ModeStrict
		if _, err := f.svc.SaveDraft(
			f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion,
			worksetusecase.SaveDraftRequest{Document: draftJSON(t, doc), IfMatchVersion: f.operation().Version},
		); err != nil {
			t.Fatalf("SaveDraft: %v", err)
		}
		_, err := f.startExecution("plan-dc", "k-dc")
		code, details := errorDetail(t, err)
		if code != "PLAN_NOT_EXECUTABLE" || !contains(details, "DRAFT_CHANGED") {
			t.Fatalf("err = %s %v, want DRAFT_CHANGED", code, details)
		}
	})

	t.Run("orphaned_workset", func(t *testing.T) {
		f := newExecFixture(t)
		f.seedRevision("plan-or")
		if err := f.repo.DeleteLibrary(f.libraryID); err != nil {
			t.Fatalf("DeleteLibrary: %v", err)
		}
		_, err := f.startExecution("plan-or", "k-or")
		code, _ := errorDetail(t, err)
		if code != "ORPHANED_WORKSET" {
			t.Fatalf("err code = %s, want ORPHANED_WORKSET", code)
		}
	})
}

// TestStartExecutionSnapshotGates covers the gates that read the frozen
// snapshot and the execution request itself.
func TestStartExecutionSnapshotGates(t *testing.T) {
	t.Run("blocked_component", func(t *testing.T) {
		f := newExecFixture(t)
		f.seedRevision("plan-bl", seedComponent{member: "albumA", partition: reconcile.PartitionMatched})
		if _, err := f.repo.DB().Exec(
			"UPDATE conversion_components SET status = 'blocked' WHERE plan_id = 'plan-bl'",
		); err != nil {
			t.Fatalf("block component: %v", err)
		}
		_, err := f.startExecution("plan-bl", "k-bl")
		code, details := errorDetail(t, err)
		if code != "PLAN_NOT_EXECUTABLE" || !contains(details, "BLOCKED_COMPONENTS") {
			t.Fatalf("err = %s %v, want BLOCKED_COMPONENTS", code, details)
		}
	})

	t.Run("input_changed_vs_frozen_fingerprint", func(t *testing.T) {
		f := newExecFixture(t)
		f.writeAudio("albumA", "00.mp3", []byte("audio-one"))
		f.seedRevision("plan-in", seedComponent{member: "albumA", partition: reconcile.PartitionMatched})
		// The disk (and the scan) moved on after the revision was frozen.
		f.writeAudio("albumA", "99.mp3", []byte("later-audio"))
		_, err := f.startExecution("plan-in", "k-in")
		code, details := errorDetail(t, err)
		if code != "PLAN_NOT_EXECUTABLE" || !contains(details, "INPUT_CHANGED") {
			t.Fatalf("err = %s %v, want INPUT_CHANGED", code, details)
		}
	})

	t.Run("zero_operation_revision_succeeds", func(t *testing.T) {
		f := newExecFixture(t)
		f.runDispatcher()
		f.writeAudio("albumA", "00.mp3", []byte("already-satisfied"))
		f.seedRevision("plan-zero", seedComponent{member: "albumA", partition: reconcile.PartitionMatched})
		started := f.mustStart("plan-zero", "k-zero")
		done := f.waitTerminal(started.ExecutionID)
		if done.Status != sqlite.ExecStatusSucceeded {
			t.Fatalf("status = %s (%s: %s), want succeeded", done.Status, done.ErrorCode, done.ErrorMessage)
		}
		if done.TotalOperations != 0 || done.CompletedOperations != 0 {
			t.Fatalf("zero-operation session reported work: %+v", done)
		}
		if len(done.Components) != 1 || done.Components[0].Status != "succeeded" {
			t.Fatalf("components = %+v", done.Components)
		}
		if _, err := os.Stat(filepath.Join(f.root, "albumA", "00.mp3")); err != nil {
			t.Fatalf("zero-operation run must not touch files: %v", err)
		}
	})
}

func TestExecutionSoftDeleteMovesAndSyncsInventory(t *testing.T) {
	f := newExecFixture(t)
	f.runDispatcher()
	source, size, mtime := f.writeAudio("albumA", "00.mp3", []byte("obsolete-audio"))
	f.seedRevision("plan-soft", seedComponent{
		id:        "comp-soft",
		member:    "albumA",
		partition: reconcile.PartitionMatched,
		ops:       []reconcile.Operation{deleteOp("comp-soft", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})

	started := f.mustStart("plan-soft", "k-soft")
	done := f.waitTerminal(started.ExecutionID)
	if done.Status != sqlite.ExecStatusSucceeded {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if mode := sessionDeleteMode(t, done.Options); mode != "soft" {
		t.Fatalf("delete mode = %s, want soft", mode)
	}
	if done.TotalOperations != 1 || done.CompletedOperations != 1 {
		t.Fatalf("progress = %d/%d, want 1/1", done.CompletedOperations, done.TotalOperations)
	}
	entry := done.Components[0]
	if entry.Status != "succeeded" || len(entry.Removed) != 1 || entry.Removed[0] != source {
		t.Fatalf("component report = %+v", entry)
	}
	if len(entry.Recovery) != 1 || !strings.HasSuffix(entry.Recovery[0], "/Delete/albumA/00.mp3") {
		t.Fatalf("soft delete must report the recovery path beside the member folder: %+v", entry.Recovery)
	}
	if !entry.InventorySynced || entry.InventorySyncError != "" {
		t.Fatalf("inventory sync = %v %q", entry.InventorySynced, entry.InventorySyncError)
	}
	if _, err := os.Stat(filepath.FromSlash(source)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("obsolete file must leave its place: %v", err)
	}
	recovered := filepath.Join(f.root, "Delete", "albumA", "00.mp3")
	content, err := os.ReadFile(recovered)
	if err != nil || string(content) != "obsolete-audio" {
		t.Fatalf("recovery copy = %q, %v", content, err)
	}
	var n int
	if err := f.repo.DB().QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", source).Scan(&n); err != nil {
		t.Fatalf("count source rows: %v", err)
	}
	if n != 0 {
		t.Fatal("the moved source row must leave the inventory")
	}
	var name string
	if err := f.repo.DB().QueryRow(
		"SELECT name FROM entries WHERE path = ?", filepath.ToSlash(recovered),
	).Scan(&name); err != nil {
		t.Fatalf("recovery row: %v", err)
	}
	if name != "00.mp3" {
		t.Fatalf("recovery row name = %q", name)
	}
}

func TestExecutionHardDeleteRemovesWithoutRecovery(t *testing.T) {
	f := newExecFixture(t)
	f.runDispatcher()
	source, size, mtime := f.writeAudio("albumB", "01.mp3", []byte("hard-delete-me"))
	// The obsolete-audio handling is a draft setting: the seeded revision's own
	// snapshot declares hard deletion.
	f.setDraftDeleteMode("hard")
	f.seedRevision("plan-hard", seedComponent{
		id:        "comp-hard",
		member:    "albumB",
		partition: reconcile.PartitionUnmatched,
		ops:       []reconcile.Operation{deleteOp("comp-hard", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})

	started := f.mustStart("plan-hard", "k-hard")
	done := f.waitTerminal(started.ExecutionID)
	if done.Status != sqlite.ExecStatusSucceeded {
		t.Fatalf("status = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if mode := sessionDeleteMode(t, done.Options); mode != "hard" {
		t.Fatalf("delete mode = %s, want hard", mode)
	}
	entry := done.Components[0]
	if len(entry.Removed) != 1 || len(entry.Recovery) != 0 {
		t.Fatalf("hard delete report = %+v", entry)
	}
	if _, err := os.Stat(filepath.FromSlash(source)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file must be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "albumB", "Delete")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hard mode must not create a recovery folder: %v", err)
	}
}

func TestExecutionComponentFailureStopsAdmission(t *testing.T) {
	f := newExecFixture(t)
	f.runDispatcher()
	first, _, _ := f.writeAudio("albumA", "00.mp3", []byte("first-file"))
	second, size2, mtime2 := f.writeAudio("albumB", "00.mp3", []byte("second-file"))
	// The frozen fact of the first component no longer matches the disk: the
	// plan was made before the file changed and the DB was never rescanned.
	f.seedRevision("plan-fail",
		seedComponent{
			id:        "comp-fail-a",
			member:    "albumA",
			partition: reconcile.PartitionMatched,
			ops:       []reconcile.Operation{deleteOp("comp-fail-a", first)},
			files:     []reconcile.FileTuple{{Path: first, Size: 3, Mtime: mtime2}},
		},
		seedComponent{
			id:        "comp-fail-b",
			member:    "albumB",
			partition: reconcile.PartitionMatched,
			ops:       []reconcile.Operation{deleteOp("comp-fail-b", second)},
			files:     []reconcile.FileTuple{{Path: second, Size: size2, Mtime: mtime2}},
		},
	)

	started := f.mustStart("plan-fail", "k-fail")
	done := f.waitTerminal(started.ExecutionID)
	if done.Status != sqlite.ExecStatusFailed {
		t.Fatalf("status = %s, want failed", done.Status)
	}
	if done.ErrorCode != "COMPONENT_FILE_CHANGED" {
		t.Fatalf("error code = %q (%s), want COMPONENT_FILE_CHANGED", done.ErrorCode, done.ErrorMessage)
	}
	if len(done.Components) != 2 {
		t.Fatalf("components = %+v", done.Components)
	}
	if done.Components[0].Status != "failed" || done.Components[0].Stage != "precheck" {
		t.Fatalf("first component = %+v", done.Components[0])
	}
	if done.Components[1].Status != "pending" {
		t.Fatalf("later component must stay unexecuted: %+v", done.Components[1])
	}
	// Nothing ran: both files are still in place.
	for _, p := range []string{first, second} {
		if _, err := os.Stat(filepath.FromSlash(p)); err != nil {
			t.Fatalf("file %s must be untouched: %v", p, err)
		}
	}
	if done.CompletedOperations != 0 {
		t.Fatalf("completed operations = %d, want 0", done.CompletedOperations)
	}
}

func TestExecutionRevisionRunsOnceAndReplaysItsKey(t *testing.T) {
	f := newExecFixture(t)
	f.runDispatcher()
	source, size, mtime := f.writeAudio("albumA", "00.mp3", []byte("once-only"))
	f.seedRevision("plan-once", seedComponent{
		id:        "comp-once",
		member:    "albumA",
		partition: reconcile.PartitionMatched,
		ops:       []reconcile.Operation{deleteOp("comp-once", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})

	first := f.mustStart("plan-once", "key-once")
	done := f.waitTerminal(first.ExecutionID)
	if done.Status != sqlite.ExecStatusSucceeded {
		t.Fatalf("status = %s (%s)", done.Status, done.ErrorMessage)
	}

	// A retry of the same request observes its own session.
	replay, err := f.startExecution("plan-once", "key-once")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Execution.ExecutionID != first.ExecutionID {
		t.Fatalf("replay = %+v", replay)
	}

	// A new key on the same revision is refused: one execution per revision.
	_, err = f.startExecution("plan-once", "key-once-2")
	code, details := errorDetail(t, err)
	if code != "PLAN_NOT_EXECUTABLE" || !contains(details, "ALREADY_EXECUTED") {
		t.Fatalf("err = %s %v, want ALREADY_EXECUTED", code, details)
	}
}

func TestExecutionConcurrentStartsHaveOneWriter(t *testing.T) {
	f := newExecFixture(t)
	source, size, mtime := f.writeAudio("albumA", "00.mp3", []byte("race"))
	f.seedRevision("plan-race", seedComponent{
		id:        "comp-race",
		member:    "albumA",
		partition: reconcile.PartitionMatched,
		ops:       []reconcile.Operation{deleteOp("comp-race", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})

	type outcome struct {
		res *worksetusecase.StartExecutionResult
		err error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for _, key := range []string{"race-1", "race-2"} {
		go func(k string) {
			<-start
			res, err := f.startExecution("plan-race", k)
			results <- outcome{res: res, err: err}
		}(key)
	}
	close(start)
	created := 0
	for range 2 {
		out := <-results
		if out.err == nil {
			created++
			continue
		}
		code, details := errorDetail(t, out.err)
		if code != "PLAN_NOT_EXECUTABLE" && code != "EXECUTION_IN_PROGRESS" {
			t.Fatalf("concurrent start error = %s %v", code, details)
		}
	}
	if created != 1 {
		t.Fatalf("concurrent starts created %d sessions, want exactly 1", created)
	}
	// Exactly one session row exists for the revision.
	var n int
	if err := f.repo.DB().QueryRow(
		"SELECT COUNT(*) FROM plan_executions WHERE plan_id = 'plan-race'",
	).Scan(&n); err != nil {
		t.Fatalf("count executions: %v", err)
	}
	if n != 1 {
		t.Fatalf("session rows = %d, want 1", n)
	}
}

func TestExecutionBlocksGenerationAndDraftEdits(t *testing.T) {
	f := newExecFixture(t)
	source, size, mtime := f.writeAudio("albumA", "00.mp3", []byte("busy"))
	f.seedRevision("plan-busy", seedComponent{
		id:        "comp-busy",
		member:    "albumA",
		partition: reconcile.PartitionMatched,
		ops:       []reconcile.Operation{deleteOp("comp-busy", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})
	// The session stays queued: the dispatcher is not started in this test.
	queued := f.mustStart("plan-busy", "k-busy")
	view := f.operation()
	if view.ActiveExecution == nil || view.ActiveExecution.ExecutionID != queued.ExecutionID {
		t.Fatalf("active execution missing from the operation view: %+v", view.ActiveExecution)
	}

	_, err := f.svc.StartGeneration(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{IfMatchVersion: f.operation().Version, IdempotencyKey: "gen-busy"},
	)
	code, _ := errorDetail(t, err)
	if code != "EXECUTION_IN_PROGRESS" {
		t.Fatalf("generation during execution: code = %s, want EXECUTION_IN_PROGRESS", code)
	}

	_, err = f.svc.SaveDraft(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{Document: f.draft().Document, IfMatchVersion: f.operation().Version},
	)
	code, _ = errorDetail(t, err)
	if code != "EXECUTION_IN_PROGRESS" {
		t.Fatalf("draft save during execution: code = %s, want EXECUTION_IN_PROGRESS", code)
	}

	// A second start of the same revision answers with the active session's
	// conflict, and canceling the queued session never runs it.
	_, err = f.startExecution("plan-busy", "k-busy-2")
	code, _ = errorDetail(t, err)
	if code != "EXECUTION_IN_PROGRESS" {
		t.Fatalf("second start: code = %s, want EXECUTION_IN_PROGRESS", code)
	}
	canceled, err := f.svc.CancelExecution(
		f.t.Context(), f.worksetID, worksetusecase.OperationTypeConversion, queued.ExecutionID,
	)
	if err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	if canceled.Status != sqlite.ExecStatusCanceled {
		t.Fatalf("canceled status = %s", canceled.Status)
	}
	if _, err := os.Stat(filepath.FromSlash(source)); err != nil {
		t.Fatalf("a canceled queued session must not touch files: %v", err)
	}
}

func TestExecutionSubscriptionSnapshotAndTerminalEvent(t *testing.T) {
	f := newExecFixture(t)
	f.runDispatcher()
	source, size, mtime := f.writeAudio("albumA", "00.mp3", []byte("streamed"))
	f.seedRevision("plan-sse", seedComponent{
		id:        "comp-sse",
		member:    "albumA",
		partition: reconcile.PartitionMatched,
		ops:       []reconcile.Operation{deleteOp("comp-sse", source)},
		files:     []reconcile.FileTuple{{Path: source, Size: size, Mtime: mtime}},
	})
	started := f.mustStart("plan-sse", "k-sse")

	type event struct {
		name string
		data map[string]any
	}
	events := make(chan event, 16)
	ctx, cancel := context.WithTimeout(f.t.Context(), 30*time.Second)
	defer cancel()
	subscribed := make(chan struct{})
	go func() {
		close(subscribed)
		_ = f.svc.SubscribeExecution(
			ctx, f.worksetID, worksetusecase.OperationTypeConversion, started.ExecutionID,
			func(name string, data any) error {
				raw, _ := json.Marshal(data)
				var m map[string]any
				_ = json.Unmarshal(raw, &m)
				events <- event{name: name, data: m}
				return nil
			},
		)
	}()
	<-subscribed

	var names []string
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-events:
			names = append(names, ev.name)
			if ev.name == "succeeded" {
				if ev.data["execution_id"] != started.ExecutionID {
					t.Fatalf("terminal event payload = %+v", ev.data)
				}
				if len(names) == 0 || names[0] != worksetusecase.ExecutionEventSnapshot {
					t.Fatalf("first event = %v, want the snapshot first", names)
				}
				return
			}
			if ev.name == "failed" || ev.name == "canceled" || ev.name == "interrupted" {
				t.Fatalf("unexpected terminal event %s: %+v", ev.name, ev.data)
			}
		case <-deadline:
			t.Fatalf("no terminal event; seen %v", names)
		}
	}
}
