package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
)

// TestObservedAudioEntriesSpansTheWholeSubtree pins the path boundaries a
// planning root is collected with: the root directory itself ("/", kept
// defensively even though no member folder can be it), a root spelled with a
// trailing slash, and a root whose audio sits several directories down. The
// range predicate is a binary one over the path index, so its edges are the
// contract — a sibling whose name merely starts with the root's name is a
// different subtree.
func TestObservedAudioEntriesSpansTheWholeSubtree(t *testing.T) {
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	now := time.Now().Format(time.RFC3339Nano)
	for _, path := range []string{
		"/music/a/01.mp3",
		"/music/a/deep/02.mp3",
		"/music/ab/03.mp3",
		"/other/04.mp3",
	} {
		if _, err := repo.DB().Exec(`
			INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format, created_at, updated_at)
			VALUES (?, ?, ?, ?, 0, 0, 0, 'mp3', ?, ?)
		`, path, filepath.Dir(path), filepath.Dir(path), filepath.Base(path), now, now); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, created_at, updated_at)
		VALUES ('/music/a/deep', '/music/a', '/music/a', 'deep', 1, 0, 0, ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed directory row: %v", err)
	}

	for _, tc := range []struct {
		root string
		want []string
	}{
		{"/", []string{"/music/a/01.mp3", "/music/a/deep/02.mp3", "/music/ab/03.mp3", "/other/04.mp3"}},
		{"/music/", []string{"/music/a/01.mp3", "/music/a/deep/02.mp3", "/music/ab/03.mp3"}},
		{"/music/a", []string{"/music/a/01.mp3", "/music/a/deep/02.mp3"}},
		{"/music/a/", []string{"/music/a/01.mp3", "/music/a/deep/02.mp3"}},
		{"/music/ab", []string{"/music/ab/03.mp3"}},
	} {
		entries, err := repo.ObservedAudioEntries(tc.root)
		if err != nil {
			t.Fatalf("collect %s: %v", tc.root, err)
		}
		got := make([]string, 0, len(entries))
		for _, e := range entries {
			got = append(got, e.Path)
		}
		if len(got) != len(tc.want) {
			t.Errorf("root %q collected %v, want %v", tc.root, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("root %q collected %v, want %v", tc.root, got, tc.want)
				break
			}
		}
	}
}
