package sqlite

import (
	"os"
	"testing"
)

func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "onsei-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(tmpFile.Name()) })
	tmpFile.Close()

	repo, err := NewRepository(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	return repo
}
