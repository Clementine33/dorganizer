package sqlite

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRepository_PlanMethods(t *testing.T) {
	t.Run("CreatePlan and GetPlan", func(t *testing.T) {
		repo := newTestRepository(t)

		slimModeVal := "2"
		plan := &Plan{
			PlanID:        "plan-001",
			RootPath:      "/music",
			PlanType:      "slim",
			SlimMode:      &slimModeVal,
			SnapshotToken: "snap-123",
			Status:        "ready",
			CreatedAt:     time.Now(),
		}

		if err := repo.CreatePlan(plan); err != nil {
			t.Fatalf("CreatePlan failed: %v", err)
		}

		fetched, err := repo.GetPlan("plan-001")
		if err != nil {
			t.Fatalf("GetPlan failed: %v", err)
		}
		if fetched.PlanType != "slim" {
			t.Errorf("expected plan_type slim, got %s", fetched.PlanType)
		}
		if fetched.SlimMode == nil || *fetched.SlimMode != "2" {
			t.Errorf("expected slim_mode 2, got %v", fetched.SlimMode)
		}
	})

	t.Run("ListPlansByRoot", func(t *testing.T) {
		repo := newTestRepository(t)

		plan := &Plan{
			PlanID:        "plan-001",
			RootPath:      "/music",
			PlanType:      "slim",
			SnapshotToken: "snap-123",
			Status:        "ready",
			CreatedAt:     time.Now(),
		}

		if err := repo.CreatePlan(plan); err != nil {
			t.Fatalf("CreatePlan failed: %v", err)
		}

		plans, err := repo.ListPlansByRoot("/music")
		if err != nil {
			t.Fatalf("ListPlansByRoot failed: %v", err)
		}
		if len(plans) != 1 {
			t.Errorf("expected 1 plan, got %d", len(plans))
		}
	})

	t.Run("UpdatePlanStatus", func(t *testing.T) {
		repo := newTestRepository(t)

		plan := &Plan{
			PlanID:        "plan-001",
			RootPath:      "/music",
			PlanType:      "slim",
			SnapshotToken: "snap-123",
			Status:        "ready",
			CreatedAt:     time.Now(),
		}

		if err := repo.CreatePlan(plan); err != nil {
			t.Fatalf("CreatePlan failed: %v", err)
		}

		if err := repo.UpdatePlanStatus("plan-001", "executed"); err != nil {
			t.Fatalf("UpdatePlanStatus failed: %v", err)
		}

		fetched, err := repo.GetPlan("plan-001")
		if err != nil {
			t.Fatalf("GetPlan failed: %v", err)
		}
		if fetched.Status != "executed" {
			t.Errorf("expected status executed, got %s", fetched.Status)
		}
	})

	t.Run("CreatePlanItem and ListPlanItems", func(t *testing.T) {
		repo := newTestRepository(t)

		plan := &Plan{
			PlanID:        "plan-001",
			RootPath:      "/music",
			PlanType:      "slim",
			SnapshotToken: "snap-123",
			Status:        "ready",
			CreatedAt:     time.Now(),
		}
		if err := repo.CreatePlan(plan); err != nil {
			t.Fatalf("CreatePlan failed: %v", err)
		}

		targetPathVal := "/music/old.mp3"
		planItem := &PlanItem{
			PlanID:                 "plan-001",
			ItemIndex:              0,
			OpType:                 "convert_and_delete",
			SourcePath:             "/music/old.wav",
			TargetPath:             &targetPathVal,
			ReasonCode:             "redundant_format",
			PreconditionPath:       "/music/old.wav",
			PreconditionContentRev: 1,
			PreconditionSize:       1024000,
			PreconditionMtime:      time.Now().Unix(),
		}

		if err := repo.CreatePlanItem(planItem); err != nil {
			t.Fatalf("CreatePlanItem failed: %v", err)
		}

		items, err := repo.ListPlanItems("plan-001")
		if err != nil {
			t.Fatalf("ListPlanItems failed: %v", err)
		}
		if len(items) != 1 {
			t.Errorf("expected 1 plan item, got %d", len(items))
		}
		if items[0].OpType != "convert_and_delete" {
			t.Errorf("expected op_type convert_and_delete, got %s", items[0].OpType)
		}
	})
}

func TestRepository_CreateAndGetPlan_SlimModeNullableRoundTrip(t *testing.T) {
	repo := newTestRepository(t)

	plan := &Plan{
		PlanID:        "plan-null-slim",
		RootPath:      "/music",
		PlanType:      "slim",
		SlimMode:      nil,
		SnapshotToken: "snap-123",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}

	if err := repo.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	fetched, err := repo.GetPlan("plan-null-slim")
	if err != nil {
		t.Fatalf("GetPlan failed: %v", err)
	}
	if fetched.SlimMode != nil {
		t.Errorf("expected slim_mode nil, got %s", *fetched.SlimMode)
	}

	slimModeVal := "2"
	plan2 := &Plan{
		PlanID:        "plan-with-slim",
		RootPath:      "/music",
		PlanType:      "slim",
		SlimMode:      &slimModeVal,
		SnapshotToken: "snap-456",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}

	if err := repo.CreatePlan(plan2); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	fetched2, err := repo.GetPlan("plan-with-slim")
	if err != nil {
		t.Fatalf("GetPlan failed: %v", err)
	}
	if fetched2.SlimMode == nil {
		t.Errorf("expected slim_mode '2', got nil")
	} else if *fetched2.SlimMode != "2" {
		t.Errorf("expected slim_mode '2', got %s", *fetched2.SlimMode)
	}
}

func TestPersistPlan_Batch_AllOrNothing(t *testing.T) {
	repo := newTestRepository(t)

	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, "/music/song1.mp3", "/music", 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	tx, err := repo.DB().Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	paths := []string{"/music/song1.mp3", "/music/nonexistent.mp3"}
	preconds, err := LoadEntryPreconditionsBatchTx(tx, paths)
	if err != nil {
		tx.Rollback()
		t.Fatalf("LoadEntryPreconditionsBatchTx failed: %v", err)
	}

	if len(preconds) != 2 {
		t.Errorf("expected 2 preconditions, got %d", len(preconds))
	}

	if p, ok := preconds["/music/song1.mp3"]; !ok {
		t.Errorf("expected precondition for /music/song1.mp3")
	} else {
		if p.ContentRev != 1 {
			t.Errorf("expected content_rev 1, got %d", p.ContentRev)
		}
		if p.Size != 1000 {
			t.Errorf("expected size 1000, got %d", p.Size)
		}
	}

	if p, ok := preconds["/music/nonexistent.mp3"]; !ok {
		t.Errorf("expected precondition for /music/nonexistent.mp3")
	} else {
		if p.ContentRev != 0 {
			t.Errorf("expected content_rev 0 for non-existent, got %d", p.ContentRev)
		}
		if p.Size != 0 {
			t.Errorf("expected size 0 for non-existent, got %d", p.Size)
		}
	}

	plan := &Plan{
		PlanID:        "test-plan-batch",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "snap-batch",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}

	if err := CreatePlanTx(tx, plan); err != nil {
		tx.Rollback()
		t.Fatalf("CreatePlanTx failed: %v", err)
	}

	items := []PlanItem{
		{
			PlanID:                 "test-plan-batch",
			ItemIndex:              0,
			OpType:                 "delete",
			SourcePath:             "/music/song1.mp3",
			TargetPath:             nil,
			ReasonCode:             "slim_delete",
			PreconditionPath:       "/music/song1.mp3",
			PreconditionContentRev: preconds["/music/song1.mp3"].ContentRev,
			PreconditionSize:       preconds["/music/song1.mp3"].Size,
			PreconditionMtime:      preconds["/music/song1.mp3"].Mtime,
		},
	}

	if err := CreatePlanItemsBatchTx(tx, "test-plan-batch", items); err != nil {
		tx.Rollback()
		t.Fatalf("CreatePlanItemsBatchTx failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit transaction: %v", err)
	}

	persistedPlan, err := repo.GetPlan("test-plan-batch")
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}
	if persistedPlan.PlanID != "test-plan-batch" {
		t.Errorf("expected plan_id test-plan-batch, got %s", persistedPlan.PlanID)
	}

	persistedItems, err := repo.ListPlanItems("test-plan-batch")
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}
	if len(persistedItems) != 1 {
		t.Errorf("expected 1 plan item, got %d", len(persistedItems))
	}
}

func TestPersistPlan_Batch_PlanIDConflict(t *testing.T) {
	repo := newTestRepository(t)

	plan1 := &Plan{
		PlanID:        "conflict-test-plan",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "snap-1",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}

	if err := repo.CreatePlan(plan1); err != nil {
		t.Fatalf("failed to create initial plan: %v", err)
	}

	targetPath := "/music/target.mp3"
	item1 := &PlanItem{
		PlanID:                 "conflict-test-plan",
		ItemIndex:              0,
		OpType:                 "convert_and_delete",
		SourcePath:             "/music/source.wav",
		TargetPath:             &targetPath,
		ReasonCode:             "redundant_format",
		PreconditionPath:       "/music/source.wav",
		PreconditionContentRev: 1,
		PreconditionSize:       2000,
		PreconditionMtime:      1234567890,
	}
	if err := repo.CreatePlanItem(item1); err != nil {
		t.Fatalf("failed to create initial plan item: %v", err)
	}

	tx, err := repo.DB().Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	plan2 := &Plan{
		PlanID:        "conflict-test-plan",
		RootPath:      "/other",
		ScanRootPath:  "/other",
		PlanType:      "prune",
		SnapshotToken: "snap-2",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}

	err = CreatePlanTx(tx, plan2)
	if err == nil {
		tx.Rollback()
		t.Fatal("expected error for duplicate plan_id, got nil")
	}

	if !IsPlanIDConflictError(err) {
		tx.Rollback()
		t.Errorf("expected PLAN_ID_CONFLICT error, got: %v", err)
	}

	tx.Rollback()

	persistedPlan, err := repo.GetPlan("conflict-test-plan")
	if err != nil {
		t.Fatalf("failed to get plan: %v", err)
	}
	if persistedPlan.RootPath != "/music" {
		t.Errorf("plan was overwritten! expected root_path /music, got %s", persistedPlan.RootPath)
	}
	if persistedPlan.PlanType != "slim" {
		t.Errorf("plan was overwritten! expected plan_type slim, got %s", persistedPlan.PlanType)
	}

	items, err := repo.ListPlanItems("conflict-test-plan")
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 plan item, got %d", len(items))
	}
	if items[0].SourcePath != "/music/source.wav" {
		t.Errorf("plan item was overwritten! expected source_path /music/source.wav, got %s", items[0].SourcePath)
	}
}

func TestPersistPlan_Batch_RollbackAfterPlanInsert(t *testing.T) {
	repo := newTestRepository(t)

	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, "/music/song.mp3", "/music", 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	planID := "test-rollback-plan"

	tx, err := repo.DB().Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	plan := &Plan{
		PlanID:        planID,
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "snap-rollback",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}

	if err := CreatePlanTx(tx, plan); err != nil {
		tx.Rollback()
		t.Fatalf("CreatePlanTx failed: %v", err)
	}

	var count int
	err = tx.QueryRow("SELECT COUNT(*) FROM plans WHERE plan_id = ?", planID).Scan(&count)
	if err != nil {
		tx.Rollback()
		t.Fatalf("failed to query plan in transaction: %v", err)
	}
	if count != 1 {
		tx.Rollback()
		t.Fatalf("expected plan to exist in transaction, got count=%d", count)
	}

	paths := []string{"/music/song.mp3"}
	preconds, err := LoadEntryPreconditionsBatchTx(tx, paths)
	if err != nil {
		tx.Rollback()
		t.Fatalf("LoadEntryPreconditionsBatchTx failed: %v", err)
	}

	items := []PlanItem{
		{
			PlanID:                 planID,
			ItemIndex:              0,
			OpType:                 "delete",
			SourcePath:             "/music/song.mp3",
			TargetPath:             nil,
			ReasonCode:             "slim_delete",
			PreconditionPath:       "/music/song.mp3",
			PreconditionContentRev: preconds["/music/song.mp3"].ContentRev,
			PreconditionSize:       preconds["/music/song.mp3"].Size,
			PreconditionMtime:      preconds["/music/song.mp3"].Mtime,
		},
	}

	if err := CreatePlanItemsBatchTx(tx, planID, items); err != nil {
		tx.Rollback()
		t.Fatalf("CreatePlanItemsBatchTx failed: %v", err)
	}

	err = tx.QueryRow("SELECT COUNT(*) FROM plan_items WHERE plan_id = ?", planID).Scan(&count)
	if err != nil {
		tx.Rollback()
		t.Fatalf("failed to query plan items in transaction: %v", err)
	}
	if count != 1 {
		tx.Rollback()
		t.Fatalf("expected 1 plan item in transaction, got count=%d", count)
	}

	tx.Rollback()

	var planCount int
	err = repo.DB().QueryRow("SELECT COUNT(*) FROM plans WHERE plan_id = ?", planID).Scan(&planCount)
	if err != nil {
		t.Fatalf("failed to query plans after rollback: %v", err)
	}
	if planCount != 0 {
		t.Errorf("rollback failed: plan row still exists (count=%d), expected 0", planCount)
	}

	var itemCount int
	err = repo.DB().QueryRow("SELECT COUNT(*) FROM plan_items WHERE plan_id = ?", planID).Scan(&itemCount)
	if err != nil {
		t.Fatalf("failed to query plan items after rollback: %v", err)
	}
	if itemCount != 0 {
		t.Errorf("rollback failed: plan items still exist (count=%d), expected 0", itemCount)
	}

	t.Logf("Rollback verified: planCount=%d, itemCount=%d (both should be 0)", planCount, itemCount)
}

func TestPersistPlan_Batch_ConstraintFailureRollback(t *testing.T) {
	repo := newTestRepository(t)

	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, "/music/song1.mp3", "/music", 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	planID := "test-constraint-rollback-plan"

	tx, err := repo.DB().Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	plan := &Plan{
		PlanID:        planID,
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "snap-constraint",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}

	if err := CreatePlanTx(tx, plan); err != nil {
		tx.Rollback()
		t.Fatalf("CreatePlanTx failed: %v", err)
	}

	var count int
	err = tx.QueryRow("SELECT COUNT(*) FROM plans WHERE plan_id = ?", planID).Scan(&count)
	if err != nil {
		tx.Rollback()
		t.Fatalf("failed to query plan in transaction: %v", err)
	}
	if count != 1 {
		tx.Rollback()
		t.Fatalf("expected plan to exist in transaction, got count=%d", count)
	}

	items := []PlanItem{
		{
			PlanID:                 planID,
			ItemIndex:              0,
			OpType:                 "delete",
			SourcePath:             "/music/song1.mp3",
			TargetPath:             nil,
			ReasonCode:             "slim_delete",
			PreconditionPath:       "/music/song1.mp3",
			PreconditionContentRev: 1,
			PreconditionSize:       1000,
			PreconditionMtime:      1234567890,
		},
		{
			PlanID:                 planID,
			ItemIndex:              0,
			OpType:                 "delete",
			SourcePath:             "/music/song2.mp3",
			TargetPath:             nil,
			ReasonCode:             "slim_delete",
			PreconditionPath:       "/music/song2.mp3",
			PreconditionContentRev: 2,
			PreconditionSize:       2000,
			PreconditionMtime:      1234567891,
		},
	}

	err = CreatePlanItemsBatchTx(tx, planID, items)
	if err == nil {
		tx.Rollback()
		t.Fatal("expected constraint violation error for duplicate item_index, got nil")
	}

	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "constraint") && !strings.Contains(errStr, "primary key") && !strings.Contains(errStr, "unique") {
		tx.Rollback()
		t.Errorf("expected constraint violation error, got: %v", err)
	}

	tx.Rollback()

	var planCount int
	err = repo.DB().QueryRow("SELECT COUNT(*) FROM plans WHERE plan_id = ?", planID).Scan(&planCount)
	if err != nil {
		t.Fatalf("failed to query plans after rollback: %v", err)
	}
	if planCount != 0 {
		t.Errorf("rollback failed: plan row still exists (count=%d), expected 0", planCount)
	}

	var itemCount int
	err = repo.DB().QueryRow("SELECT COUNT(*) FROM plan_items WHERE plan_id = ?", planID).Scan(&itemCount)
	if err != nil {
		t.Fatalf("failed to query plan items after rollback: %v", err)
	}
	if itemCount != 0 {
		t.Errorf("rollback failed: plan items still exist (count=%d), expected 0", itemCount)
	}

	t.Logf("Constraint failure rollback verified: planCount=%d, itemCount=%d (both should be 0)", planCount, itemCount)
}

func TestPersistPlan_Batch_ChunkedPreconditions(t *testing.T) {
	repo := newTestRepository(t)

	numPaths := 1500
	paths := make([]string, numPaths)
	expectedPaths := make(map[string]bool)

	for i := 0; i < numPaths; i++ {
		paths[i] = fmt.Sprintf("/music/song_%04d.mp3", i)
		if i%3 == 0 {
			_, err := repo.DB().Exec(`
				INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
				VALUES (?, ?, 0, ?, 'audio/mpeg', ?, ?)
			`, paths[i], "/music", int64(i*1000), i, int64(1234567890+i))
			if err != nil {
				t.Fatalf("failed to insert entry %d: %v", i, err)
			}
			expectedPaths[paths[i]] = true
		}
	}

	tx, err := repo.DB().Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	preconds, err := LoadEntryPreconditionsBatchTx(tx, paths)
	if err != nil {
		t.Fatalf("loadEntryPreconditionsBatchTx failed: %v", err)
	}

	if len(preconds) != numPaths {
		t.Errorf("expected %d preconditions, got %d", numPaths, len(preconds))
	}

	for _, path := range paths {
		if _, ok := preconds[path]; !ok {
			t.Errorf("missing precondition for path %s", path)
		}
	}

	for path := range expectedPaths {
		p := preconds[path]
		if p.ContentRev == 0 && p.Size == 0 && !strings.Contains(path, "song_0000") {
			t.Errorf("existing entry %s should have non-zero values (got rev=%d, size=%d)", path, p.ContentRev, p.Size)
		}
	}
}

func TestPersistPlan_Batch_ChunkedItems(t *testing.T) {
	repo := newTestRepository(t)

	plan := &Plan{
		PlanID:        "chunked-items-plan",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "snap-chunked",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatalf("failed to create plan: %v", err)
	}

	numItems := 750
	items := make([]PlanItem, numItems)
	for i := 0; i < numItems; i++ {
		items[i] = PlanItem{
			PlanID:                 "chunked-items-plan",
			ItemIndex:              i,
			OpType:                 "delete",
			SourcePath:             fmt.Sprintf("/music/song_%04d.mp3", i),
			TargetPath:             nil,
			ReasonCode:             "slim_delete",
			PreconditionPath:       fmt.Sprintf("/music/song_%04d.mp3", i),
			PreconditionContentRev: i,
			PreconditionSize:       int64(i * 1000),
			PreconditionMtime:      int64(1234567890 + i),
		}
	}

	tx, err := repo.DB().Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	if err := CreatePlanItemsBatchTx(tx, "chunked-items-plan", items); err != nil {
		tx.Rollback()
		t.Fatalf("CreatePlanItemsBatchTx failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit transaction: %v", err)
	}

	persistedItems, err := repo.ListPlanItems("chunked-items-plan")
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	if len(persistedItems) != numItems {
		t.Errorf("expected %d plan items, got %d", numItems, len(persistedItems))
	}

	for i, item := range persistedItems {
		if item.ItemIndex != i {
			t.Errorf("item %d: expected index %d, got %d", i, i, item.ItemIndex)
		}
	}
}

func TestRepository_ListPlanItems_DeleteTargetPathRoundTrip(t *testing.T) {
	repo := newTestRepository(t)

	plan := &Plan{
		PlanID:        "plan-multi-target",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "snap-456",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	convertTarget := "/music/song.m4a"
	convertItem := &PlanItem{
		PlanID:                 "plan-multi-target",
		ItemIndex:              0,
		OpType:                 "convert_and_delete",
		SourcePath:             "/music/song.flac",
		TargetPath:             &convertTarget,
		ReasonCode:             "convert",
		PreconditionPath:       "/music/song.flac",
		PreconditionContentRev: 1,
		PreconditionSize:       2048000,
		PreconditionMtime:      time.Now().Unix(),
	}

	deleteTarget := "/music/Delete/album/track.mp3"
	deleteItem := &PlanItem{
		PlanID:                 "plan-multi-target",
		ItemIndex:              1,
		OpType:                 "delete",
		SourcePath:             "/music/album/track.mp3",
		TargetPath:             &deleteTarget,
		ReasonCode:             "slim_delete",
		PreconditionPath:       "/music/album/track.mp3",
		PreconditionContentRev: 1,
		PreconditionSize:       1024000,
		PreconditionMtime:      time.Now().Unix(),
	}

	deleteItemNull := &PlanItem{
		PlanID:                 "plan-multi-target",
		ItemIndex:              2,
		OpType:                 "delete",
		SourcePath:             "/music/album/track2.mp3",
		TargetPath:             nil,
		ReasonCode:             "hard_delete",
		PreconditionPath:       "/music/album/track2.mp3",
		PreconditionContentRev: 1,
		PreconditionSize:       1024000,
		PreconditionMtime:      time.Now().Unix(),
	}

	if err := repo.CreatePlanItem(convertItem); err != nil {
		t.Fatalf("CreatePlanItem convert failed: %v", err)
	}
	if err := repo.CreatePlanItem(deleteItem); err != nil {
		t.Fatalf("CreatePlanItem delete failed: %v", err)
	}
	if err := repo.CreatePlanItem(deleteItemNull); err != nil {
		t.Fatalf("CreatePlanItem delete null failed: %v", err)
	}

	itemsOut, err := repo.ListPlanItems("plan-multi-target")
	if err != nil {
		t.Fatalf("ListPlanItems failed: %v", err)
	}
	if len(itemsOut) != 3 {
		t.Fatalf("expected 3 items, got %d", len(itemsOut))
	}

	if itemsOut[0].TargetPath == nil || *itemsOut[0].TargetPath != convertTarget {
		t.Errorf("convert item: expected target_path %q, got %v", convertTarget, itemsOut[0].TargetPath)
	}

	if itemsOut[1].TargetPath == nil || *itemsOut[1].TargetPath != deleteTarget {
		t.Errorf("delete item: expected target_path %q, got %v", deleteTarget, itemsOut[1].TargetPath)
	}

	if itemsOut[2].TargetPath != nil {
		t.Errorf("delete null item: expected target_path nil, got %q", *itemsOut[2].TargetPath)
	}
}
