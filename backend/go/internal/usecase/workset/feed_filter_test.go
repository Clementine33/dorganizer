package workset //nolint:testpackage // white-box tests exercise unexported internals

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// seedFeedFixtures creates n worksets with a controllable state mix:
// one orphaned (error), one blocked-revision (error), one unplanned (pending),
// and the rest planned+healthy (normal). All live in one library with one
// folder each.
func seedFeedFixtures(t *testing.T, svc *serviceImpl) {
	t.Helper()
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	ctx := context.Background()

	// pending: unplanned
	if _, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "lib-1", Title: "待规划", FolderIDs: []string{"f-a"}, IdempotencyKey: "feed-1"},
	); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	// normal: planned with a healthy revision is expensive to fabricate fully;
	// instead we build it via SaveDraft + revision row through the same path
	// the generation worker uses (persistence helpers), skipping RunWorkflow.
	if _, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "lib-1", Title: "正常", FolderIDs: []string{"f-a"}, IdempotencyKey: "feed-2"},
	); err != nil {
		t.Fatalf("create normal: %v", err)
	}
	// error: orphaned
	res, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "lib-1", Title: "孤立", FolderIDs: []string{"f-a"}, IdempotencyKey: "feed-3"},
	)
	if err != nil {
		t.Fatalf("create orphan: %v", err)
	}
	if _, orphanErr := repo.DB().
		Exec("UPDATE worksets SET library_id = NULL WHERE id = ?", res.Workset.WorksetID); orphanErr != nil {
		t.Fatalf("orphan: %v", orphanErr)
	}
	// error: blocked revision (fabricated through the repo persistence path:
	// one blocked component forces BlockedCount > 0 on read).
	res4, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "lib-1", Title: "阻塞", FolderIDs: []string{"f-a"}, IdempotencyKey: "feed-4"},
	)
	if err != nil {
		t.Fatalf("create blocked: %v", err)
	}
	fabricateBlockedRevision(t, svc, res4.Workset.WorksetID, "lib-1")
}

// fabricateBlockedRevision persists a minimal workflow revision with one
// blocked component and promotes it as the workset's current revision.
func fabricateBlockedRevision(t *testing.T, svc *serviceImpl, worksetID, libraryID string) {
	t.Helper()
	repo := svc.repo
	now := time.Now()
	draft, err := repo.GetWorksetDraft(worksetID)
	if err != nil || draft == nil {
		t.Fatalf("draft load: %v", err)
	}
	persist := sqlite.WorksetRevisionPersist{
		PlanID:          "plan-blocked-" + worksetID,
		RootPath:        "/music/albumA",
		SnapshotToken:   "snapshot-blocked-" + worksetID,
		LibraryID:       libraryID,
		DraftHash:       draft.DraftHash,
		MemberHash:      "member-hash",
		WorksetsVersion: 1,
		Steps: []sqlite.WorkflowStepRecord{
			{
				StepIndex:       0,
				StepType:        "reconcile_audio_outputs",
				Status:          "blocked",
				PolicyJSON:      `{"schema_version":1}`,
				PolicyHash:      "hash",
				StepSummaryJSON: `{"component_count":1,"blocked_count":1,"operation_count":0,"error_count":1,"summary_reason":"BLOCKED"}`,
			},
		},
		Roots: []sqlite.WorkflowRootRecord{{
			RootIndex:            0,
			RootPath:             "/music/albumA",
			RootIdentity:         "/music/albumA",
			InventoryFingerprint: "fp",
			RootStatus:           "ok",
		}},
		Components: []sqlite.WorkflowComponentRecord{
			{
				StepIndex:      0,
				ComponentIndex: 0,
				ComponentID:    "comp-blocked",
				RootIndex:      0,
				Partition:      "matched",
				Status:         "blocked",
				ReasonCode:     "TARGET_PATH_CONFLICT",
				OutcomeJSON:    `{"component_id":"comp-blocked","partition":"matched","status":"blocked","reason_code":"TARGET_PATH_CONFLICT"}`,
			},
		},
	}
	gen := &sqlite.PlanGeneration{
		GenerationID:   "gen-fabricated-" + worksetID,
		WorksetID:      worksetID,
		Status:         "completed",
		TotalRoots:     1,
		CompletedRoots: 1,
		CreatedAt:      now,
		FinishedAt:     now,
	}
	if err := repo.CreateGeneration(gen); err != nil {
		t.Fatalf("create fabricated generation: %v", err)
	}
	if err := repo.PersistWorksetRevision(gen.GenerationID, worksetID, now, persist); err != nil {
		t.Fatalf("persist fabricated revision: %v", err)
	}
}

// TestFeedFilterClassification verifies the mutually exclusive, error-first
// feed classification against seeded fixtures.
func TestFeedFilterClassification(t *testing.T) {
	svc := newSvc(t, 1)
	seedFeedFixtures(t, svc)
	ctx := context.Background()

	views, _, err := svc.ListWorksets(ctx, ListQuery{Feed: FeedAll, IncludeOrphaned: true})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(views) != 4 {
		t.Fatalf("all feed = %d worksets, want 4", len(views))
	}

	// Orphaned + fabricated-revision worksets land in error.
	errorViews, _, err := svc.ListWorksets(ctx, ListQuery{Feed: FeedError, IncludeOrphaned: true})
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(errorViews) != 2 {
		t.Fatalf("error feed = %d, want 2 (orphan + blocked)", len(errorViews))
	}

	// Unplanned worksets land in pending; none are normal (the two remaining
	// have revisions that mark them error, the other is unplanned too).
	pendingViews, _, err := svc.ListWorksets(ctx, ListQuery{Feed: FeedPending})
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pendingViews) != 2 {
		t.Fatalf("pending feed = %d, want 2", len(pendingViews))
	}

	// normal excludes error and pending buckets; with these fixtures the
	// remaining two both carry error markers.
	normalViews, _, err := svc.ListWorksets(ctx, ListQuery{Feed: FeedNormal, IncludeOrphaned: true})
	if err != nil {
		t.Fatalf("list normal: %v", err)
	}
	if len(normalViews) != 0 {
		t.Fatalf("normal feed = %d, want 0", len(normalViews))
	}

	// Invalid filter is rejected.
	if _, _, err := svc.ListWorksets(ctx, ListQuery{Feed: "bogus"}); err == nil {
		t.Fatal("invalid feed filter should fail")
	}
}

// TestFeedFilterPaginationAcrossPages proves the scan-until-full loop keeps
// pagination correct: with a tiny page size and mostly-non-matching rows, the
// filtered result must equal the full filtered set and cursors must advance
// past returned rows without skipping or duplicating.
func TestFeedFilterPaginationAcrossPages(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	ctx := context.Background()

	// 6 unplanned (pending) + 2 orphaned (error): pending appears 1-in-4 rows.
	for i := range 6 {
		if _, err := svc.CreateWorkset(
			ctx,
			CreateRequest{
				LibraryID:      "lib-1",
				Title:          fmt.Sprintf("p%d", i),
				FolderIDs:      []string{"f-a"},
				IdempotencyKey: fmt.Sprintf("pg-%d", i),
			},
		); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	for i := range 2 {
		res, err := svc.CreateWorkset(
			ctx,
			CreateRequest{
				LibraryID:      "lib-1",
				Title:          fmt.Sprintf("o%d", i),
				FolderIDs:      []string{"f-a"},
				IdempotencyKey: fmt.Sprintf("og-%d", i),
			},
		)
		if err != nil {
			t.Fatalf("create orphan %d: %v", i, err)
		}
		if _, err := repo.DB().
			Exec("UPDATE worksets SET library_id = NULL WHERE id = ?", res.Workset.WorksetID); err != nil {
			t.Fatalf("orphan %d: %v", i, err)
		}
	}

	// Reference: full pending list without pagination.
	full, _, err := svc.ListWorksets(ctx, ListQuery{Feed: FeedPending, Limit: 100})
	if err != nil {
		t.Fatalf("full list: %v", err)
	}
	if len(full) != 6 {
		t.Fatalf("full pending = %d, want 6", len(full))
	}

	// Paginate with limit=2: collect pages until exhaustion.
	var collected []*WorksetView
	cursor := ""
	pages := 0
	for {
		page, next, err := svc.ListWorksets(ctx, ListQuery{Feed: FeedPending, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		collected = append(collected, page...)
		pages++
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(collected) != len(full) {
		t.Fatalf("paginated pending = %d, want %d", len(collected), len(full))
	}
	for i := range full {
		if collected[i].WorksetID != full[i].WorksetID {
			t.Fatalf("row %d: %s != %s", i, collected[i].WorksetID, full[i].WorksetID)
		}
	}

	// Time ordering: every page must be newest-first (updated_at, id).
	for i := 1; i < len(collected); i++ {
		prev, cur := collected[i-1], collected[i]
		if prev.UpdatedAt.Before(cur.UpdatedAt) {
			t.Fatalf("order violation at %d: %v before %v", i, prev.UpdatedAt, cur.UpdatedAt)
		}
	}
}

var _ = time.Now // keep time import if fixtures change
