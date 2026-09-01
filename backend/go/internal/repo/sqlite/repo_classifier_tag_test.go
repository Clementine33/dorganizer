package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func TestClassifierTagLibraryCRUD(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "tags.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	defer repo.Close()

	// 1. Initial tag library is empty
	tags, err := repo.GetClassifierTags()
	if err != nil {
		t.Fatalf("get tags: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected 0 tags initially, got %d", len(tags))
	}

	// 2. Add tag
	created, err := repo.AddClassifierTag("  SEなし  ")
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if created.Tag != "SEなし" || created.NormalizedTag != "seなし" {
		t.Fatalf("unexpected tag created: %+v", created)
	}

	// 3. Add duplicate (case-insensitive) -> idempotent
	dup, err := repo.AddClassifierTag("seなし")
	if err != nil {
		t.Fatalf("add duplicate: %v", err)
	}
	if dup.ID != created.ID {
		t.Fatalf("expected same ID on duplicate, got %d vs %d", dup.ID, created.ID)
	}

	// 4. Add second tag
	_, err = repo.AddClassifierTag("反転")
	if err != nil {
		t.Fatalf("add second tag: %v", err)
	}

	list, err := repo.GetClassifierTags()
	if err != nil {
		t.Fatalf("get list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(list))
	}

	// 5. Delete tag
	if err := repo.DeleteClassifierTag(created.ID); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	listAfterDel, err := repo.GetClassifierTags()
	if err != nil {
		t.Fatalf("get list after del: %v", err)
	}
	if len(listAfterDel) != 1 || listAfterDel[0].Tag != "反転" {
		t.Fatalf("unexpected list after deletion: %+v", listAfterDel)
	}
}
