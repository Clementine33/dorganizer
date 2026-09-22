package fileops_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/fileops"
)

// fixture is a real library root with real members, since the whole point of
// this package is what it does to a real directory tree.
type fixture struct {
	t         *testing.T
	root      string
	refreshed []string
	scanErr   error
	svc       *fileops.Service
	gate      *admission.Gate
}

func newFixture(t *testing.T, tasksActive bool) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root}
	if tasksActive {
		f.gate = admission.NewGate(func() (bool, error) { return true, nil })
	} else {
		f.gate = admission.NewGate(func() (bool, error) { return false, nil })
	}
	f.svc = fileops.NewService(f.gate, func(_ context.Context, folderPath, _ string) error {
		f.refreshed = append(f.refreshed, folderPath)
		return f.scanErr
	})
	return f
}

func (f *fixture) mkdir(rel string) string {
	f.t.Helper()
	abs := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(abs, 0o755); err != nil {
		f.t.Fatalf("mkdir %s: %v", rel, err)
	}
	return abs
}

func (f *fixture) write(rel, content string) string {
	f.t.Helper()
	abs := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		f.t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

func (f *fixture) apply(req fileops.Request) *fileops.Result {
	f.t.Helper()
	req.LibraryRoot = f.root
	result, err := f.svc.Apply(context.Background(), req)
	if err != nil {
		f.t.Fatalf("Apply(%s): %v", req.Operation, err)
	}
	return result
}

func (f *fixture) exists(rel string) bool {
	f.t.Helper()
	_, err := os.Lstat(filepath.Join(f.root, filepath.FromSlash(rel)))
	return err == nil
}

func (f *fixture) read(rel string) string {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(rel)))
	if err != nil {
		f.t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

func oneItem(result *fileops.Result) fileops.ItemResult {
	if len(result.Items) != 1 {
		panic("expected exactly one item result")
	}
	return result.Items[0]
}

// TestRenameFileAndDirectory covers the rename contract: one item, a new name,
// the content in place under the new name.
func TestRenameFileAndDirectory(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "audio")
	f.mkdir("albumA/disc2")
	f.write("albumA/disc2/02.mp3", "nested")

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpRename,
		Items:      []fileops.Item{{Source: "01.mp3", Name: "track01.mp3"}},
	})
	item := oneItem(result)
	if item.Status != fileops.StatusOK || item.Target != "track01.mp3" {
		t.Fatalf("item = %+v", item)
	}
	if f.exists("albumA/01.mp3") || !f.exists("albumA/track01.mp3") {
		t.Fatal("the file was not renamed")
	}
	if got := f.read("albumA/track01.mp3"); got != "audio" {
		t.Fatalf("content = %q", got)
	}

	// Directories rename the same way, children included.
	result = f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpRename,
		Items:      []fileops.Item{{Source: "disc2", Name: "disc02"}},
	})
	if item := oneItem(result); item.Status != fileops.StatusOK {
		t.Fatalf("directory rename = %+v", item)
	}
	if !f.exists("albumA/disc02/02.mp3") {
		t.Fatal("renaming a directory must carry its children")
	}
	if len(f.refreshed) != 2 || filepath.Base(f.refreshed[0]) != "albumA" {
		t.Fatalf("refreshed = %v, want one refresh of the member per request", f.refreshed)
	}
}

// TestRenameRefusals covers the shapes a name may not have and the target
// that must not be overwritten.
func TestRenameRefusals(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")
	f.write("albumA/taken.mp3", "two")

	cases := []struct {
		name      string
		item      fileops.Item
		code      string
		untouched string
	}{
		{
			"a name is not a path",
			fileops.Item{Source: "01.mp3", Name: "sub/01.mp3"},
			fileops.CodeInvalidName,
			"albumA/01.mp3",
		},
		{"traversal name", fileops.Item{Source: "01.mp3", Name: ".."}, fileops.CodeInvalidName, "albumA/01.mp3"},
		{"target exists", fileops.Item{Source: "01.mp3", Name: "taken.mp3"}, fileops.CodeTargetExists, "albumA/01.mp3"},
		{"source missing", fileops.Item{Source: "gone.mp3", Name: "new.mp3"}, fileops.CodeSourceMissing, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := f.apply(fileops.Request{
				MemberPath: "albumA",
				Operation:  fileops.OpRename,
				Items:      []fileops.Item{tc.item},
			})
			item := oneItem(result)
			if item.Status != fileops.StatusFailed || item.Code != tc.code {
				t.Fatalf("item = %+v, want failed %s", item, tc.code)
			}
			if tc.untouched != "" && !f.exists(tc.untouched) {
				t.Fatalf("%s must be untouched by a refused rename", tc.untouched)
			}
			if tc.code == fileops.CodeTargetExists && f.read("albumA/taken.mp3") != "two" {
				t.Fatal("a refused rename must not overwrite the target")
			}
		})
	}
}

// TestMoveInsideTheMember covers the move contract and its refusals.
func TestMoveInsideTheMember(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")
	f.mkdir("albumA/sub")
	f.mkdir("albumA/sub/deeper")

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpMove,
		Items:      []fileops.Item{{Source: "01.mp3", TargetDir: "sub"}},
	})
	if item := oneItem(result); item.Status != fileops.StatusOK || item.Target != "sub/01.mp3" {
		t.Fatalf("move = %+v", item)
	}
	if !f.exists("albumA/sub/01.mp3") || f.exists("albumA/01.mp3") {
		t.Fatal("the file did not move")
	}

	// A move exists only inside the member: the target directory must exist
	// and must not be the item's own subtree.
	cases := []struct {
		name string
		item fileops.Item
		code string
	}{
		{"missing target directory", fileops.Item{Source: "sub/01.mp3", TargetDir: "nope"}, fileops.CodeInvalidTarget},
		{"target is a file", fileops.Item{Source: "sub/01.mp3", TargetDir: "sub/01.mp3"}, fileops.CodeInvalidTarget},
		{"into itself", fileops.Item{Source: "sub", TargetDir: "sub"}, fileops.CodeMoveIntoSelf},
		{"into its own descendant", fileops.Item{Source: "sub", TargetDir: "sub/deeper"}, fileops.CodeMoveIntoSelf},
		{"no destination", fileops.Item{Source: "sub/01.mp3"}, fileops.CodeInvalidTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := f.apply(fileops.Request{
				MemberPath: "albumA",
				Operation:  fileops.OpMove,
				Items:      []fileops.Item{tc.item},
			})
			if item := oneItem(result); item.Status != fileops.StatusFailed || item.Code != tc.code {
				t.Fatalf("item = %+v, want failed %s", item, tc.code)
			}
		})
	}
	if !f.exists("albumA/sub/01.mp3") || !f.exists("albumA/sub/deeper") {
		t.Fatal("refused moves must leave the tree as it was")
	}
}

// TestSoftDeleteIntoTheLibraryRecoveryDirectory covers the recycling contract:
// the item keeps its path relative to the library root, the recovery location
// is reported, and an earlier recovery is never overwritten.
func TestSoftDeleteIntoTheLibraryRecoveryDirectory(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "first")
	f.mkdir("albumA/disc")
	f.write("albumA/disc/02.mp3", "nested")
	// Media recycled earlier, at the same relative path.
	f.write("Delete/albumA/01.mp3", "earlier")

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpSoftDelete,
		Items: []fileops.Item{
			{Source: "01.mp3"},
			{Source: "disc"},
		},
	})
	if result.Succeeded != 2 || result.Failed != 0 || result.Untouched != 0 {
		t.Fatalf("counts = %+v", result)
	}
	if result.Items[0].RecoveredPath != "Delete/albumA/01.1.mp3" {
		t.Fatalf("collision naming = %q, want Delete/albumA/01.1.mp3", result.Items[0].RecoveredPath)
	}
	if result.Items[1].RecoveredPath != "Delete/albumA/disc" {
		t.Fatalf("directory recovery = %q", result.Items[1].RecoveredPath)
	}
	if f.read("Delete/albumA/01.mp3") != "earlier" {
		t.Fatal("recycling overwrote media recycled earlier")
	}
	if f.read("Delete/albumA/01.1.mp3") != "first" || !f.exists("Delete/albumA/disc/02.mp3") {
		t.Fatal("recycled content is not where the result says it is")
	}
	if f.exists("albumA/01.mp3") || f.exists("albumA/disc") {
		t.Fatal("the member still holds what was deleted")
	}
	if !result.Refresh.OK {
		t.Fatalf("refresh = %+v", result.Refresh)
	}
	// Both the member and the recovery tree were refreshed.
	sort.Strings(f.refreshed)
	if len(f.refreshed) != 2 || filepath.Base(f.refreshed[0]) != fileops.RecoveryDir ||
		filepath.Base(f.refreshed[1]) != "albumA" {
		t.Fatalf("refreshed = %v, want the member and the recovery directory", f.refreshed)
	}
}

// TestBatchStopsAtTheFirstFailure covers the batch rule: the first failure ends the run,
// what already happened is reported as done, and what never ran is named as
// not attempted.
func TestBatchStopsAtTheFirstFailure(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")
	f.write("albumA/03.mp3", "three")

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpSoftDelete,
		Items: []fileops.Item{
			{Source: "01.mp3"},
			{Source: "gone.mp3"},
			{Source: "03.mp3"},
		},
	})
	if result.Succeeded != 1 || result.Failed != 1 || result.Untouched != 1 {
		t.Fatalf("counts = %+v (items %+v)", result, result.Items)
	}
	if result.Items[0].Status != fileops.StatusOK {
		t.Fatalf("first item = %+v", result.Items[0])
	}
	if result.Items[1].Status != fileops.StatusFailed || result.Items[1].Code != fileops.CodeSourceMissing {
		t.Fatalf("second item = %+v", result.Items[1])
	}
	if result.Items[2].Status != fileops.StatusNotAttempted {
		t.Fatalf("third item = %+v, want not attempted", result.Items[2])
	}
	if !f.exists("albumA/03.mp3") {
		t.Fatal("the item after the failure must not have run")
	}
	if !f.exists("Delete/albumA/01.mp3") {
		t.Fatal("what already succeeded is never rolled back")
	}
}

// TestParentAndChildAreNotProcessedTwice covers the de-duplication: selecting
// a directory and something inside it is one operation, not two.
func TestParentAndChildAreNotProcessedTwice(t *testing.T) {
	f := newFixture(t, false)
	f.mkdir("albumA/disc")
	f.write("albumA/disc/01.mp3", "one")

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpSoftDelete,
		Items: []fileops.Item{
			{Source: "disc/01.mp3"},
			{Source: "disc"},
		},
	})
	if result.Succeeded != 1 || result.Untouched != 1 {
		t.Fatalf("counts = %+v (items %+v)", result, result.Items)
	}
	if result.Items[0].Status != fileops.StatusSkipped || result.Items[0].Code != fileops.CodeCoveredByParent {
		t.Fatalf("child item = %+v", result.Items[0])
	}
	if result.Items[1].Status != fileops.StatusOK {
		t.Fatalf("parent item = %+v", result.Items[1])
	}
	if !f.exists("Delete/albumA/disc/01.mp3") {
		t.Fatal("the parent's recycle must carry the whole subtree")
	}
}

// TestRepeatedItemIsProcessedOnce keeps a batch honest about what it did: the
// same path named twice is one operation, and the second mention says so
// instead of looking like a failure.
func TestRepeatedItemIsProcessedOnce(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpSoftDelete,
		Items:      []fileops.Item{{Source: "01.mp3"}, {Source: "01.mp3"}},
	})
	if result.Succeeded != 1 || result.Failed != 0 || result.Untouched != 1 {
		t.Fatalf("counts = %+v (items %+v)", result, result.Items)
	}
	if result.Items[1].Status != fileops.StatusSkipped || result.Items[1].Code != fileops.CodeDuplicateItem {
		t.Fatalf("second mention = %+v", result.Items[1])
	}
	if !f.exists("Delete/albumA/01.mp3") {
		t.Fatal("the item was not recycled once")
	}
}

// TestRefusedScopes covers the paths the service refuses before touching the
// disk: escapes, links, the member root, and the recovery directory.
func TestRefusedScopes(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")
	f.mkdir("albumA/sub")

	if err := os.Symlink(filepath.Join(f.root, "albumA"), filepath.Join(f.root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(f.root, "outside"), filepath.Join(f.root, "albumA", "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f.write("outside/01.mp3", "outside")

	cases := []struct {
		name       string
		memberPath string
		item       fileops.Item
		code       string
	}{
		{"escape the member", "albumA", fileops.Item{Source: "../outside/01.mp3"}, fileops.CodePathInvalid},
		{"absolute path", "albumA", fileops.Item{Source: "/etc/passwd"}, fileops.CodePathInvalid},
		{"through a link", "albumA", fileops.Item{Source: "escape/01.mp3"}, fileops.CodeSymlink},
		{"the link itself", "albumA", fileops.Item{Source: "escape"}, fileops.CodeSymlink},
		{"nothing at all", "albumA", fileops.Item{Source: ""}, fileops.CodePathInvalid},
		{"the member root as an item", "albumA", fileops.Item{Source: "."}, fileops.CodePathInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := f.apply(fileops.Request{
				MemberPath: tc.memberPath,
				Operation:  fileops.OpSoftDelete,
				Items:      []fileops.Item{tc.item},
			})
			if item := oneItem(result); item.Status != fileops.StatusFailed || item.Code != tc.code {
				t.Fatalf("item = %+v, want failed %s", item, tc.code)
			}
		})
	}
	if !f.exists("outside/01.mp3") {
		t.Fatal("a refused path must not move anything outside the member")
	}
	if !f.exists("albumA/escape") {
		t.Fatal("a refused item must not be moved")
	}
}

// TestRefusedMembers covers the directories that are not members.
func TestRefusedMembers(t *testing.T) {
	f := newFixture(t, false)
	f.mkdir("albumA")
	f.mkdir(fileops.RecoveryDir)

	cases := []struct {
		name   string
		member string
		code   string
	}{
		{"nested path", "albumA/sub", fileops.CodePathInvalid},
		{"traversal", "../albumA", fileops.CodePathInvalid},
		{"absolute", "", fileops.CodePathInvalid},
		{"the recovery directory", fileops.RecoveryDir, fileops.CodeMemberRoot},
		{"missing directory", "gone", fileops.CodeMemberMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.Apply(context.Background(), fileops.Request{
				LibraryRoot: f.root,
				MemberPath:  tc.member,
				Operation:   fileops.OpSoftDelete,
				Items:       []fileops.Item{{Source: "01.mp3"}},
			})
			if err == nil {
				t.Fatal("expected the request to be refused")
			}
			var pathErr *fileops.PathError
			if !errors.As(err, &pathErr) || pathErr.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

// TestSymlinkedMemberIsNotAMember pins the link rule for the member itself: a
// link is never followed, so nothing below it is reachable through the
// workbench.
func TestSymlinkedMemberIsNotAMember(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege on Windows")
	}
	f := newFixture(t, false)
	f.mkdir("real")
	f.write("real/01.mp3", "one")
	if err := os.Symlink(filepath.Join(f.root, "real"), filepath.Join(f.root, "albumA")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := f.svc.Apply(context.Background(), fileops.Request{
		LibraryRoot: f.root,
		MemberPath:  "albumA",
		Operation:   fileops.OpSoftDelete,
		Items:       []fileops.Item{{Source: "01.mp3"}},
	})
	var pathErr *fileops.PathError
	if !errors.As(err, &pathErr) || pathErr.Code != fileops.CodeSymlink {
		t.Fatalf("err = %v, want %s", err, fileops.CodeSymlink)
	}
	if !f.exists("real/01.mp3") {
		t.Fatal("nothing may be written through a link")
	}
}

// TestUnknownOperationIsRefused covers the vocabulary: only the three
// operations exist, and an unknown one never reaches the disk.
func TestUnknownOperationIsRefused(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")
	_, err := f.svc.Apply(context.Background(), fileops.Request{
		LibraryRoot: f.root,
		MemberPath:  "albumA",
		Operation:   "chmod",
		Items:       []fileops.Item{{Source: "01.mp3"}},
	})
	var pathErr *fileops.PathError
	if !errors.As(err, &pathErr) || pathErr.Code != fileops.CodeOperationDenied {
		t.Fatalf("err = %v, want %s", err, fileops.CodeOperationDenied)
	}
	if !f.exists("albumA/01.mp3") {
		t.Fatal("nothing may have happened")
	}
}

// TestRenameAndMoveTakeOneItem pins the request shape: a rename and a move act
// on one item and a multi-item request is refused before anything is written,
// while a soft delete is the one operation that takes a batch.
func TestRenameAndMoveTakeOneItem(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")
	f.write("albumA/02.mp3", "two")

	batches := []struct {
		operation string
		items     []fileops.Item
	}{
		{
			fileops.OpRename,
			[]fileops.Item{{Source: "01.mp3", Name: "a.mp3"}, {Source: "02.mp3", Name: "b.mp3"}},
		},
		{
			fileops.OpMove,
			[]fileops.Item{{Source: "01.mp3", TargetDir: "sub"}, {Source: "02.mp3", TargetDir: "sub"}},
		},
	}
	for _, tc := range batches {
		t.Run(tc.operation, func(t *testing.T) {
			_, err := f.svc.Apply(context.Background(), fileops.Request{
				LibraryRoot: f.root,
				MemberPath:  "albumA",
				Operation:   tc.operation,
				Items:       tc.items,
			})
			var pathErr *fileops.PathError
			if !errors.As(err, &pathErr) || pathErr.Code != fileops.CodePathInvalid {
				t.Fatalf("err = %v, want a refused request (%s)", err, fileops.CodePathInvalid)
			}
			if !f.exists("albumA/01.mp3") || !f.exists("albumA/02.mp3") {
				t.Fatal("a refused request must leave every item where it was")
			}
		})
	}

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpSoftDelete,
		Items:      []fileops.Item{{Source: "01.mp3"}, {Source: "02.mp3"}},
	})
	if result.Succeeded != 2 {
		t.Fatalf("counts = %+v, want both items recycled", result)
	}
}

// TestRefreshFailureIsReportedSeparately covers the refresh rule: the writes happened, so a
// failed refresh is reported as a refresh failure and never as a failed write.
func TestRefreshFailureIsReportedSeparately(t *testing.T) {
	f := newFixture(t, false)
	f.write("albumA/01.mp3", "one")
	f.scanErr = errors.New("scan exploded")

	result := f.apply(fileops.Request{
		MemberPath: "albumA",
		Operation:  fileops.OpSoftDelete,
		Items:      []fileops.Item{{Source: "01.mp3"}},
	})
	if result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("counts = %+v, want the write reported as done", result)
	}
	if result.Refresh.OK || result.Refresh.Code != "REFRESH_FAILED" {
		t.Fatalf("refresh = %+v, want a reported refresh failure", result.Refresh)
	}
	if !strings.Contains(result.Refresh.Message, "files were modified") {
		t.Fatalf("the refresh failure must say the files were modified: %q", result.Refresh.Message)
	}
	if !f.exists("Delete/albumA/01.mp3") {
		t.Fatal("the write itself must have happened")
	}
}

// TestRequestsWithoutItemsAreRefused keeps an empty batch from looking like a
// successful operation.
func TestRequestsWithoutItemsAreRefused(t *testing.T) {
	f := newFixture(t, false)
	f.mkdir("albumA")
	if _, err := f.svc.Apply(context.Background(), fileops.Request{
		LibraryRoot: f.root,
		MemberPath:  "albumA",
		Operation:   fileops.OpSoftDelete,
	}); err == nil {
		t.Fatal("an empty request must be refused")
	}
}
