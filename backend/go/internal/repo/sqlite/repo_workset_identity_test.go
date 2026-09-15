package sqlite //nolint:testpackage // legacy-DB bootstrap needs the internal timeFormat constant

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// openLegacyWorksetDB builds a database file with the pre-operation workset
// schema — workset_members without member_id, plus the retired workset_drafts,
// workset_revisions and workset_confirmations tables and a legacy planning
// session — and reopens it through NewRepository. Legacy aggregates must stay
// readable at the database level while the new operation tables come up empty:
// ADR 0004 requires no legacy-data migration and forbids deleting user data.
func openLegacyWorksetDB(t *testing.T, members []string) (*Repository, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy-worksets.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()
	ddl := []string{
		`CREATE TABLE libraries (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			root_path TEXT NOT NULL DEFAULT '',
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE worksets (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
			root_path TEXT NOT NULL DEFAULT '',
			root_path_key TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			current_revision_id TEXT,
			creation_idem_key TEXT,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE workset_members (
			workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
			member_index INTEGER NOT NULL,
			rel_path TEXT NOT NULL,
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			folder_name TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (workset_id, member_index),
			UNIQUE (workset_id, rel_path)
		)`,
		`CREATE TABLE workset_drafts (
			workset_id TEXT PRIMARY KEY REFERENCES worksets(id) ON DELETE CASCADE,
			workflow_schema_version INTEGER NOT NULL DEFAULT 1,
			steps_json TEXT NOT NULL DEFAULT '[]',
			draft_hash TEXT NOT NULL DEFAULT '',
			updated_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE workset_revisions (
			plan_id TEXT PRIMARY KEY,
			workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
			revision_index INTEGER NOT NULL,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (workset_id, revision_index)
		)`,
		`CREATE TABLE workset_confirmations (
			plan_id TEXT PRIMARY KEY REFERENCES workset_revisions(plan_id) ON DELETE CASCADE,
			workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
			confirmed_version INTEGER NOT NULL DEFAULT 0,
			confirmed_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO libraries (id, name, root_path) VALUES ('lib-1', 'L', '/music')`,
		`INSERT INTO worksets (id, title, library_id, root_path, root_path_key, version, current_revision_id)
			VALUES ('ws-1', 'legacy', 'lib-1', '/music', '/music', 4, '')`,
		`INSERT INTO workset_drafts (workset_id, steps_json, draft_hash, updated_at)
			VALUES ('ws-1', '[{"step_type":"reconcile_audio_outputs"}]', 'legacy-hash', '2026-01-01T00:00:00Z')`,
	}
	for _, stmt := range ddl {
		if _, execErr := db.Exec(stmt); execErr != nil {
			t.Fatalf("legacy ddl: %v", execErr)
		}
	}
	for i, rel := range members {
		if _, execErr := db.Exec(
			`INSERT INTO workset_members (workset_id, member_index, rel_path) VALUES ('ws-1', ?, ?)`,
			i, rel,
		); execErr != nil {
			t.Fatalf("insert legacy member: %v", execErr)
		}
	}
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository on legacy workset schema: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo, dbPath
}

func TestLegacyWorksetDatabaseOpensAndBackfillsMemberIDs(t *testing.T) {
	repo, dbPath := openLegacyWorksetDB(t, []string{"albumA", "albumB"})

	first, err := repo.ListWorksetMembers("ws-1")
	if err != nil {
		t.Fatalf("ListWorksetMembers: %v", err)
	}
	if len(first) != 2 || first[0].MemberID == "" || first[1].MemberID == "" {
		t.Fatalf("member_id not backfilled: %+v", first)
	}
	if first[0].MemberID == first[1].MemberID {
		t.Fatalf("backfilled identities collide: %q", first[0].MemberID)
	}

	// The workset row itself still reads, and the new operation tables are
	// present but empty: no legacy aggregate was migrated or deleted.
	w, err := repo.GetWorkset("ws-1")
	if err != nil || w.Version != 4 {
		t.Fatalf("legacy workset: %+v err=%v", w, err)
	}
	if _, opErr := repo.GetOperation("ws-1", "conversion"); opErr == nil {
		t.Fatal("legacy worksets must not be silently upgraded to an operation")
	}
	var legacyDrafts int
	if countErr := repo.db.QueryRow("SELECT COUNT(*) FROM workset_drafts").Scan(&legacyDrafts); countErr != nil {
		t.Fatalf("count legacy drafts: %v", countErr)
	}
	if legacyDrafts != 1 {
		t.Fatalf("legacy draft rows were touched: %d", legacyDrafts)
	}

	// Reopening the same database must never regenerate identities.
	if closeErr := repo.Close(); closeErr != nil {
		t.Fatalf("close repo: %v", closeErr)
	}
	reopened, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	second, err := reopened.ListWorksetMembers("ws-1")
	if err != nil {
		t.Fatalf("reread members: %v", err)
	}
	for i := range first {
		if first[i].MemberID != second[i].MemberID {
			t.Fatalf("identity changed on reopen: %q -> %q", first[i].MemberID, second[i].MemberID)
		}
		if first[i].RelPath != second[i].RelPath {
			t.Fatalf("member order changed on reopen: %q -> %q", first[i].RelPath, second[i].RelPath)
		}
	}
	// An operation can be established on the legacy workset afterwards without
	// disturbing the members.
	now := time.Now()
	if createErr := reopened.CreateWorkset(
		&Workset{
			ID:          "ws-new",
			Title:       "新",
			LibraryID:   "lib-1",
			RootPath:    "/music",
			RootPathKey: "/music",
			Version:     1,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		nil,
		[]Operation{{WorksetID: "ws-new", OperationType: "conversion", Version: 1, CreatedAt: now, UpdatedAt: now}},
		[]OperationDraft{{
			WorksetID:     "ws-new",
			OperationType: "conversion",
			SchemaVersion: 1,
			DraftJSON:     `{}`,
			DraftHash:     "h",
			UpdatedAt:     now,
		}},
	); createErr != nil {
		t.Fatalf("create on legacy database: %v", createErr)
	}
}

// TestCanonicalJSONHashIgnoresKeyOrder pins the canonical hash contract shared
// by draft saves and dedup comparisons.
func TestCanonicalJSONHashIgnoresKeyOrder(t *testing.T) {
	a := `{"schema_version":1,"mode":"strict","classifier_tags":["SEなし"],"matched":{"lossless":{"codec":"wav"}}}`
	b := `{"matched":{"lossless":{"codec":"wav"}},"classifier_tags":["SEなし"],"mode":"strict","schema_version":1}`
	if CanonicalJSONHash([]byte(a)) != CanonicalJSONHash([]byte(b)) {
		t.Fatal("key order changed the canonical hash")
	}
	c := `{"matched":{"lossless":{"codec":"flac"}},"classifier_tags":["SEなし"],"mode":"strict","schema_version":1}`
	if CanonicalJSONHash([]byte(a)) == CanonicalJSONHash([]byte(c)) {
		t.Fatal("different content must hash differently")
	}
}
