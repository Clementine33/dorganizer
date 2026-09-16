package workset_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

const timeFmt = "2006-01-02T15:04:05.999999999Z07:00"

// fixture wires a real repository and workset service on a temp database with
// its own config directory. Every test drives the service through its exported
// API only.
type fixture struct {
	t    *testing.T
	repo *sqlite.Repository
	svc  worksetusecase.Service
	ctx  context.Context

	startOnce sync.Once
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithScan(t, nil)
}

// newFixtureWithScan wires the session-time folder refresh seam (the disk →
// inventory path every session uses); a nil scan keeps the stored inventory as
// the only input facts, which most tests want.
func newFixtureWithScan(t *testing.T, scan worksetusecase.FolderScan) *fixture {
	t.Helper()
	tmp := t.TempDir()
	repo, err := sqlite.NewRepository(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	cfg := `{"prune":{"literal_tags":["SEなし"]}}`
	if err := os.WriteFile(filepath.Join(tmp, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return &fixture{
		t:    t,
		repo: repo,
		svc: worksetusecase.NewService(repo, 1, []worksetusecase.Task{
			tasksconversion.New(tmp),
		}, scan),
		ctx: context.Background(),
	}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.repo.DB().Exec(query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *fixture) insertLibrary(id, root string) {
	f.t.Helper()
	now := time.Now().Format(timeFmt)
	f.exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, id, root, root, now, now)
}

func (f *fixture) insertFolder(libID, id, path, rel, name string) {
	f.t.Helper()
	now := time.Now().Format(timeFmt)
	f.exec(`
		INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?)
	`, id, libID, path, name, rel, now, now)
}

func (f *fixture) insertAudioEntry(path, root string, size, mtime int64) {
	f.t.Helper()
	now := time.Now().Format(timeFmt)
	f.exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, 'mp3', ?, ?)
	`, path, root, filepath.Dir(path), filepath.Base(path), size, mtime, now, now)
}

func (f *fixture) insertNonAudioEntry(path, root string, size, mtime int64) {
	f.t.Helper()
	now := time.Now().Format(timeFmt)
	f.exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, 'txt', ?, ?)
	`, path, root, filepath.Dir(path), filepath.Base(path), size, mtime, now, now)
}

// standardLibrary creates one library with the given folders and returns the
// folder ids in order.
func (f *fixture) standardLibrary(folderNames ...string) []string {
	f.t.Helper()
	f.insertLibrary("lib-1", "/music")
	ids := make([]string, 0, len(folderNames))
	for i, name := range folderNames {
		id := "f-" + name
		f.insertFolder("lib-1", id, "/music/"+name, name, name)
		ids = append(ids, id)
		_ = i
	}
	return ids
}

func (f *fixture) createWorkset(title string, folderIDs ...string) *worksetusecase.WorksetView {
	f.t.Helper()
	res, err := f.svc.CreateWorkset(f.ctx, worksetusecase.CreateRequest{
		LibraryID: "lib-1", Title: title, FolderIDs: folderIDs,
	})
	if err != nil {
		f.t.Fatalf("CreateWorkset: %v", err)
	}
	return res.Workset
}

// mustDraft parses the task's opaque draft payload for assertions.
func mustDraft(t *testing.T, d *worksetusecase.Draft) *tasksconversion.DraftDoc {
	t.Helper()
	doc, err := tasksconversion.ParseDraft(string(d.Document))
	if err != nil {
		t.Fatalf("parse draft: %v", err)
	}
	return doc
}

// draftJSON encodes a draft document as the task's opaque payload.
func draftJSON(t *testing.T, doc *tasksconversion.DraftDoc) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal draft: %v", err)
	}
	return raw
}

func (f *fixture) operation(worksetID string) *worksetusecase.OperationView {
	f.t.Helper()
	view, err := f.svc.GetOperation(f.ctx, worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		f.t.Fatalf("GetOperation: %v", err)
	}
	return view
}

func (f *fixture) draft(worksetID string) *worksetusecase.Draft {
	f.t.Helper()
	d, err := f.svc.GetDraft(f.ctx, worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		f.t.Fatalf("GetDraft: %v", err)
	}
	return d
}

func (f *fixture) saveDraft(
	worksetID string,
	doc *tasksconversion.DraftDoc,
	ifMatch int,
) *worksetusecase.OperationView {
	f.t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		f.t.Fatalf("marshal draft: %v", err)
	}
	view, err := f.svc.SaveDraft(
		f.ctx,
		worksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.SaveDraftRequest{
			Document:       raw,
			IfMatchVersion: ifMatch,
		},
	)
	if err != nil {
		f.t.Fatalf("SaveDraft: %v", err)
	}
	return view
}

// startGeneration enqueues a session and returns its id.
func (f *fixture) startGeneration(worksetID string, ifMatch int) string {
	f.t.Helper()
	res, err := f.svc.StartGeneration(
		f.ctx,
		worksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: ifMatch,
			IdempotencyKey: "gen-" + time.Now().Format("150405.000000000"),
		},
	)
	if err != nil {
		f.t.Fatalf("StartGeneration: %v", err)
	}
	if res.Generation == nil {
		f.t.Fatalf("no session in fresh start result: %+v", res)
	}
	return res.Generation.GenerationID
}

// runGeneration enqueues a session and drives the dispatcher until the session
// reaches a terminal status. Real workers and real SQLite: the wait is on
// actual process events, so it polls instead of synchronizing in-process.
func (f *fixture) runGeneration(worksetID string, ifMatch int) *worksetusecase.GenerationView {
	f.t.Helper()
	genID := f.startGeneration(worksetID, ifMatch)
	f.startOnce.Do(func() {
		f.svc.DispatcherHandle().Start()
		f.t.Cleanup(f.svc.DispatcherHandle().Stop)
	})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		g, err := f.svc.GetGeneration(f.ctx, worksetID, worksetusecase.OperationTypeConversion, genID)
		if err != nil {
			f.t.Fatalf("GetGeneration: %v", err)
		}
		switch g.Status {
		case sqlite.GenStatusCompleted, sqlite.GenStatusFailed, sqlite.GenStatusCanceled, sqlite.GenStatusInterrupted:
			return g
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("generation %s did not finish in time", genID)
	return nil
}

// profile is the wav + mp3@320 output profile used across fixtures.
func profile() reconcile.DesiredProfile {
	return reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded: &reconcile.AudioOutputSpec{
			Codec:   reconcile.CodecMp3,
			Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
		},
	}
}

// draftDoc is a complete, generatable common configuration.
func draftDoc() *tasksconversion.DraftDoc {
	return &tasksconversion.DraftDoc{
		SchemaVersion:  tasksconversion.DraftSchemaVersion,
		Mode:           reconcile.ModeAvailableSources,
		ClassifierTags: []string{"SEなし"},
		Matched:        profile(),
		Unmatched:      profile(),
	}
}
