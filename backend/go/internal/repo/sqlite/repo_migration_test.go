package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// openLegacyLibraryDB builds a database with the pre-key libraries schema and
// the given (id, root_path) rows, then reopens it through NewRepository to
// exercise the root_path_key migration.
func openLegacyLibraryDB(t *testing.T, rows [][2]string) (*Repository, error) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE libraries (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		root_path TEXT NOT NULL DEFAULT '',
		created_at TEXT DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
		last_scan_at TEXT,
		last_scan_status TEXT NOT NULL DEFAULT '',
		last_scan_error TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create legacy libraries table: %v", err)
	}
	for _, r := range rows {
		if _, err := db.Exec(
			"INSERT INTO libraries (id, name, root_path) VALUES (?, 'Legacy', ?)",
			r[0],
			r[1],
		); err != nil {
			t.Fatalf("insert legacy library: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}
	return NewRepository(dbPath)
}

func TestMigrateLibraryRootPathKeysBackfills(t *testing.T) {
	repo, err := openLegacyLibraryDB(t, [][2]string{
		{"lib-1", "/music"},
		{"lib-2", `C:\Music`},
	})
	if err != nil {
		t.Fatalf("NewRepository on legacy schema failed: %v", err)
	}
	defer repo.Close()

	keys := map[string]string{}
	rows, err := repo.DB().Query("SELECT id, root_path_key FROM libraries")
	if err != nil {
		t.Fatalf("query root_path_key: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			t.Fatal(err)
		}
		keys[id] = key
	}
	if keys["lib-1"] != "/music" {
		t.Errorf("lib-1 key = %q, want /music", keys["lib-1"])
	}
	if keys["lib-2"] != "c:/music" {
		t.Errorf("lib-2 key = %q, want c:/music", keys["lib-2"])
	}

	// Uniqueness against the backfilled keys must hold for new writes.
	if _, err := repo.CreateLibrary("Three", "/music/."); !errors.Is(err, ErrLibraryExists) {
		t.Errorf("expected ErrLibraryExists for `/music/.` against backfilled key, got %v", err)
	}
}

func TestMigrateLibraryRootPathKeysDetectsCollision(t *testing.T) {
	_, err := openLegacyLibraryDB(t, [][2]string{
		{"lib-1", "/music"},
		{"lib-2", "/music/."},
	})
	if err == nil {
		t.Fatal("expected migration to fail on canonical root collision")
	}
	if !strings.Contains(err.Error(), "canonicalization collision") {
		t.Errorf("expected collision diagnostic, got: %v", err)
	}
}

// openLegacyPlansDB builds a database with the pre-library_id plans schema
// plus a libraries table, then reopens it through NewRepository.
func openLegacyPlansDB(t *testing.T) (*Repository, error) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy-plans.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy plans db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE libraries (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		root_path TEXT NOT NULL DEFAULT '',
		created_at TEXT DEFAULT CURRENT_TIMESTAMP,
		updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
		last_scan_at TEXT,
		last_scan_status TEXT NOT NULL DEFAULT '',
		last_scan_error TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create legacy libraries table: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE plans (
		plan_id TEXT PRIMARY KEY,
		root_path TEXT NOT NULL,
		scan_root_path TEXT NOT NULL DEFAULT '',
		plan_type TEXT NOT NULL,
		slim_mode TEXT,
		snapshot_token TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'ready',
		created_at TEXT DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create legacy plans table: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO libraries (id, name, root_path) VALUES ('lib-1', 'Music', '/music')",
	); err != nil {
		t.Fatalf("insert legacy library: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, snapshot_token, status) VALUES ('plan-1', '/music/Album', '/music', 'slim', 'snap-1', 'ready')",
	); err != nil {
		t.Fatalf("insert plan-1: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, snapshot_token, status) VALUES ('plan-2', '/other', '/other', 'slim', 'snap-2', 'ready')",
	); err != nil {
		t.Fatalf("insert plan-2: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy plans db: %v", err)
	}
	return NewRepository(dbPath)
}

// TestWorkflowMigrationPurgesLegacyPlans is the breaking migration contract:
// opening a pre-workflow database purges every legacy plan row and the
// per-plan/execute intermediate state, while the workflow schema is created and
// new workflow plans round-trip. Libraries, entries and scans survive (covered
// implicitly by NewRepository succeeding and a new workflow plan listing).
func TestWorkflowMigrationPurgesLegacyPlans(t *testing.T) {
	repo, err := openLegacyPlansDB(t)
	if err != nil {
		t.Fatalf("NewRepository on legacy plans schema failed: %v", err)
	}
	defer repo.Close()

	// Legacy plans are intermediate-state only: all purged.
	plans, err := repo.ListPlans(nil, 100)
	if err != nil {
		t.Fatalf("ListPlans failed: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("expected 0 legacy plans after migration, got %d", len(plans))
	}
	if _, err := repo.GetPlan("plan-1"); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("GetPlan(plan-1) = %v, want ErrPlanNotFound", err)
	}
	if _, err := repo.GetWorkflowPlanDetail("plan-1"); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("GetWorkflowPlanDetail(plan-1) = %v, want ErrPlanNotFound", err)
	}

	// A new workflow plan round-trips after migration.
	lib, err := repo.CreateLibrary("New", "/new")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}
	err = CreateWorkflowPlanTx(
		repo.DB(),
		"wf-1",
		"workflow",
		"/new",
		"snap-wf",
		lib.ID,
		[]WorkflowStepRecord{{
			StepIndex: 0, StepType: "reconcile_audio_outputs", Status: "ok",
			PolicySchemaVersion: 1, PolicyJSON: `{"schema_version":1}`, PolicyHash: "h",
			ClassifierTags: "se\xe3\x81\xaa\xe3\x81\x97", ClassifierHash: "ch",
			StepSummaryJSON: `{"summary_reason":"NO_MATCH"}`,
		}},
		[]WorkflowRootRecord{
			{RootIndex: 0, RootPath: "/new", RootIdentity: "/new", InventoryFingerprint: "fp", EntryCount: 0},
		},
		[]WorkflowComponentRecord{{
			ComponentIndex: 0, ComponentID: "cid", RootIndex: 0, Partition: "unmatched",
			Status: "ok", OutcomeJSON: `{}`,
		}},
	)
	if err != nil {
		t.Fatalf("CreateWorkflowPlanTx failed: %v", err)
	}
	detail, err := repo.GetWorkflowPlanDetail("wf-1")
	if err != nil {
		t.Fatalf("GetWorkflowPlanDetail(wf-1) failed: %v", err)
	}
	if len(detail.Steps) != 1 || len(detail.Components) != 1 || len(detail.Roots) != 1 {
		t.Fatalf(
			"workflow detail steps=%d roots=%d components=%d",
			len(detail.Steps),
			len(detail.Roots),
			len(detail.Components),
		)
	}
	if detail.Plan.PlanKind != "workflow" || detail.Plan.WorkflowSchemaVersion != 1 {
		t.Fatalf("plan kind=%q schema=%d", detail.Plan.PlanKind, detail.Plan.WorkflowSchemaVersion)
	}

	// Execute boundary guard sees the workflow plan.
	kind, schema, err := repo.GetPlanWorkflowSchema("wf-1")
	if err != nil || kind != "workflow" || schema != 1 {
		t.Fatalf("GetPlanWorkflowSchema = %q/%d/%v", kind, schema, err)
	}
}
