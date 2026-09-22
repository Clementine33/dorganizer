package workset_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
	"github.com/onsei/organizer/backend/internal/workset"
)

const timeFmt = "2006-01-02T15:04:05.999999999Z07:00"

// fixture wires a real repository and workset service on a temp database with
// its own config directory. Every test drives the service through its exported
// API only.
type fixture struct {
	t    *testing.T
	repo *sqlite.Repository
	svc  workset.Service
	ctx  context.Context

	startOnce sync.Once
	// placeholders are the fixture's own audio rows, removed once a record is
	// created from them.
	placeholders []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithScan(t, nil)
}

// newFixtureWithScan wires the session-time folder refresh seam (the disk →
// inventory path every session uses); a nil scan keeps the stored inventory as
// the only input facts, which most tests want.
func newFixtureWithScan(t *testing.T, scan workset.FolderScan) *fixture {
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
		svc: workset.NewService(repo, 1, 1, []workset.Task{
			tasksconversion.New(tmp, repo),
		}, scan, nil),
		ctx: context.Background(),
	}
}

// queryInt reads one integer from the fixture's database.
func (f *fixture) queryInt(dest *int, query string, args ...any) {
	f.t.Helper()
	if err := f.repo.DB().QueryRow(query, args...).Scan(dest); err != nil {
		f.t.Fatalf("query %q: %v", query, err)
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

// insertDir seeds one directory of the scanned inventory. A member directory
// exists in the inventory the same way a scan would leave it: the directory
// row, and at least one audio file beneath it.
//
// A placeholder audio row is remembered rather than left behind: a record can
// only be created from a directory the inventory knows to hold audio, and the
// tests that assert on a plan's contents want the inventory they built
// themselves. clearPlaceholders removes them once the record exists.
func (f *fixture) insertDir(root, rel string, withAudio bool) {
	f.t.Helper()
	abs := root + "/" + rel
	f.insertDirEntry(abs, root, rel)
	if withAudio {
		placeholder := abs + "/__member__.flac"
		f.insertAudioEntry(placeholder, root, 1024, 0)
		f.placeholders = append(f.placeholders, placeholder)
	}
}

// ensurePlaceholders restores the fixture's placeholder audio rows, which a
// later creation in the same test needs again.
func (f *fixture) ensurePlaceholders() {
	f.t.Helper()
	for _, path := range f.placeholders {
		f.exec(`
			INSERT OR IGNORE INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format)
			VALUES (?, ?, ?, '__member__.flac', 0, 1024, 0, 'flac')
		`, path, f.rootOf(path), f.parentOf(path))
	}
}

// clearPlaceholders drops the fixture's own placeholder audio rows, so the
// inventory that remains is exactly what the test wrote.
func (f *fixture) clearPlaceholders() {
	f.t.Helper()
	for _, path := range f.placeholders {
		f.exec("DELETE FROM entries WHERE path = ?", path)
	}
}

// rootOf is the library root a placeholder path belongs to.
func (f *fixture) rootOf(path string) string {
	for _, root := range []string{"/music", "/music2"} {
		if len(path) > len(root) && path[:len(root)+1] == root+"/" {
			return root
		}
	}
	return "/music"
}

// parentOf is the directory a placeholder path sits in.
func (f *fixture) parentOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return path
}

func (f *fixture) insertDirEntry(abs, root, name string) {
	f.t.Helper()
	now := time.Now().Format(timeFmt)
	f.exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 0, 0, '', ?, ?)
	`, abs, root, root, name, now, now)
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

// standardLibrary creates one library with the given member directories,
// seeded into the scanned inventory with audio, and returns their
// library-relative paths in order.
func (f *fixture) standardLibrary(folderNames ...string) []string {
	f.t.Helper()
	f.insertLibrary("lib-1", "/music")
	rels := make([]string, 0, len(folderNames))
	for _, name := range folderNames {
		f.insertDir("/music", name, true)
		rels = append(rels, name)
	}
	return rels
}

// createCurrent creates the library's current record for the conversion
// operation and returns its view.
func (f *fixture) createCurrent(title string, relPaths ...string) *workset.WorksetView {
	f.t.Helper()
	return f.createCurrentFor("lib-1", title, relPaths...)
}

// createCurrentFor is createCurrent for a named library, so a test can hold
// records of two libraries at once.
func (f *fixture) createCurrentFor(libraryID, title string, relPaths ...string) *workset.WorksetView {
	f.t.Helper()
	f.ensurePlaceholders()
	view, err := f.svc.CreateCurrentWorkset(f.ctx, workset.CreateCurrentRequest{
		LibraryID:         libraryID,
		OperationType:     workset.OperationTypeConversion,
		Title:             title,
		FolderPaths:       relPaths,
		ExpectedCurrentID: f.currentID(libraryID),
		IdempotencyKey:    "create-" + title,
	})
	if err != nil {
		f.t.Fatalf("CreateCurrentWorkset: %v", err)
	}
	f.clearPlaceholders()
	return view.Workset
}

// createCurrentOperation creates a record for the given operation type, so a
// test can drive the generic seam with another task.
func (f *fixture) createCurrentOperation(
	libraryID, operationType string,
	relPaths ...string,
) *workset.WorksetView {
	f.t.Helper()
	f.ensurePlaceholders()
	view, err := f.svc.CreateCurrentWorkset(f.ctx, workset.CreateCurrentRequest{
		LibraryID:         libraryID,
		OperationType:     operationType,
		FolderPaths:       relPaths,
		ExpectedCurrentID: f.currentIDFor(libraryID, operationType),
		IdempotencyKey:    "create-" + operationType,
	})
	if err != nil {
		f.t.Fatalf("CreateCurrentWorkset(%s): %v", operationType, err)
	}
	f.clearPlaceholders()
	return view.Workset
}

// currentIDFor is the current record id of one (library, operation) pair.
func (f *fixture) currentIDFor(libraryID, operationType string) string {
	f.t.Helper()
	view, err := f.svc.GetCurrentWorkset(f.ctx, libraryID, operationType)
	if err != nil {
		f.t.Fatalf("GetCurrentWorkset(%s): %v", operationType, err)
	}
	if view == nil {
		return ""
	}
	return view.WorksetID
}

// currentID is the id of the library's current record, "" when it has none.
func (f *fixture) currentID(libraryID string) string {
	f.t.Helper()
	view, err := f.svc.GetCurrentWorkset(f.ctx, libraryID, workset.OperationTypeConversion)
	if err != nil {
		f.t.Fatalf("GetCurrentWorkset: %v", err)
	}
	if view == nil {
		return ""
	}
	return view.WorksetID
}

// mustDraft parses the task's opaque draft payload for assertions.
func mustDraft(t *testing.T, d *workset.Draft) *tasksconversion.DraftDoc {
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

func (f *fixture) operation(worksetID string) *workset.OperationView {
	f.t.Helper()
	view, err := f.svc.GetOperation(f.ctx, worksetID, workset.OperationTypeConversion)
	if err != nil {
		f.t.Fatalf("GetOperation: %v", err)
	}
	return view
}

func (f *fixture) draft(worksetID string) *workset.Draft {
	f.t.Helper()
	d, err := f.svc.GetDraft(f.ctx, worksetID, workset.OperationTypeConversion)
	if err != nil {
		f.t.Fatalf("GetDraft: %v", err)
	}
	return d
}

func (f *fixture) saveDraft(
	worksetID string,
	doc *tasksconversion.DraftDoc,
	ifMatch int,
) *workset.OperationView {
	f.t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		f.t.Fatalf("marshal draft: %v", err)
	}
	view, err := f.svc.SaveDraft(
		f.ctx,
		worksetID,
		workset.OperationTypeConversion,
		workset.SaveDraftRequest{
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
		workset.OperationTypeConversion,
		workset.StartGenerationRequest{
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
func (f *fixture) runGeneration(worksetID string, ifMatch int) *workset.GenerationView {
	f.t.Helper()
	genID := f.startGeneration(worksetID, ifMatch)
	f.startOnce.Do(func() {
		f.svc.DispatcherHandle().Start()
		f.t.Cleanup(f.svc.DispatcherHandle().Stop)
	})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		g, err := f.svc.GetGeneration(f.ctx, worksetID, workset.OperationTypeConversion, genID)
		if err != nil {
			f.t.Fatalf("GetGeneration: %v", err)
		}
		switch g.Status {
		case workset.GenStatusCompleted,
			workset.GenStatusFailed,
			workset.GenStatusCanceled,
			workset.GenStatusInterrupted:
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
