package grpc

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

func TestPlanOperations_MultiFolder_GroupNaturalSortByRelativeName(t *testing.T) {
	tmpDir := t.TempDir()

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	scanRoot := filepath.Join(tmpDir, "scan-root")
	folders := map[string]string{
		"folder20": filepath.Join(scanRoot, "A", "folder20"),
		"folder10": filepath.Join(scanRoot, "A", "folder10"),
		"folder3":  filepath.Join(scanRoot, "A", "folder3"),
		"folder2":  filepath.Join(scanRoot, "A", "folder2"),
		"folder1":  filepath.Join(scanRoot, "A", "folder1"),
	}

	for name, folder := range folders {
		file := filepath.Join(folder, "track-"+name+".flac")
		mustWriteFile(t, file)
		insertSlimEntry(t, repo, file, scanRoot, "audio/flac", 2000, nil)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType: "slim",
		FolderPaths: []string{
			folders["folder20"],
			folders["folder10"],
			folders["folder3"],
			folders["folder2"],
			folders["folder1"],
		},
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	if len(resp.GetPlanErrors()) > 0 {
		t.Fatalf("expected no plan_errors, got %+v", resp.GetPlanErrors())
	}

	got := folderSequenceFromOps(t, resp.GetOperations())
	want := []string{"folder1", "folder2", "folder3", "folder10", "folder20"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected folder group order from operations: got=%v want=%v", got, want)
	}
}

func TestPlanOperations_MultiFolder_InGroupDepthThenNaturalSort(t *testing.T) {
	tmpDir := t.TempDir()

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	scanRoot := filepath.Join(tmpDir, "scan-root")
	focusFolder := filepath.Join(scanRoot, "B", "folder2")
	otherFolder := filepath.Join(scanRoot, "B", "folder10")

	filesInReverse := []string{
		"album/disc10/d10.flac",
		"album/disc2/d2.flac",
		"2-mp3/n1.flac",
		"1-mp3/m10.flac",
		"1-mp3/m2.flac",
		"1-mp3/m1.flac",
		"root10.wav",
		"root2.wav",
		"root1.wav",
	}
	for _, rel := range filesInReverse {
		full := filepath.Join(focusFolder, filepath.FromSlash(rel))
		mustWriteFile(t, full)
		insertSlimEntry(t, repo, full, scanRoot, "audio/flac", 2000, nil)
	}

	otherFile := filepath.Join(otherFolder, "other.flac")
	mustWriteFile(t, otherFile)
	insertSlimEntry(t, repo, otherFile, scanRoot, "audio/flac", 2000, nil)

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:    "slim",
		FolderPaths: []string{otherFolder, focusFolder},
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	if len(resp.GetPlanErrors()) > 0 {
		t.Fatalf("expected no plan_errors, got %+v", resp.GetPlanErrors())
	}

	got := operationRelPathsForFolder(t, resp.GetOperations(), focusFolder)
	want := []string{
		"root1.wav",
		"root2.wav",
		"root10.wav",
		"1-mp3/m1.flac",
		"1-mp3/m2.flac",
		"1-mp3/m10.flac",
		"2-mp3/n1.flac",
		"album/disc2/d2.flac",
		"album/disc10/d10.flac",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected in-group operation source order: got=%v want=%v", got, want)
	}
}

func TestPlanOperations_MultiFolder_OrderDeterministicAcrossRuns(t *testing.T) {
	tmpDir := t.TempDir()

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	scanRoot := filepath.Join(tmpDir, "scan-root")
	folder1 := filepath.Join(scanRoot, "A", "folder1")
	folder2 := filepath.Join(scanRoot, "A", "folder2")
	folder3 := filepath.Join(scanRoot, "A", "folder3") // failing folder
	folder10 := filepath.Join(scanRoot, "A", "folder10")

	for _, tc := range []struct {
		folder string
		file   string
	}{
		{folder: folder10, file: "track10.flac"},
		{folder: folder2, file: "track2.flac"},
		{folder: folder1, file: "track1.flac"},
	} {
		full := filepath.Join(tc.folder, tc.file)
		mustWriteFile(t, full)
		insertSlimEntry(t, repo, full, tc.folder, "audio/flac", 2000, nil)
	}

	if err := os.MkdirAll(folder3, 0o755); err != nil {
		t.Fatalf("mkdir failing folder: %v", err)
	}
	insertSlimEntry(t, repo, filepath.ToSlash(folder3)+"/bad\x00track.mp3", folder3, "audio/mpeg", 1000, int64(192000))

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	var baseline *determinismSnapshot
	for i := 0; i < 12; i++ {
		request := &pb.PlanOperationsRequest{
			PlanType: "slim",
			FolderPaths: []string{
				folder10,
				folder3,
				folder2,
				folder1,
			},
		}

		if i > 0 {
			if _, clearErr := repo.DB().Exec("DELETE FROM plan_items"); clearErr != nil {
				t.Fatalf("failed to clear plan_items before run %d: %v", i+1, clearErr)
			}
			if _, clearErr := repo.DB().Exec("DELETE FROM plans"); clearErr != nil {
				t.Fatalf("failed to clear plans before run %d: %v", i+1, clearErr)
			}
		}

		resp, callErr := server.PlanOperations(nil, request)
		if callErr != nil {
			t.Fatalf("PlanOperations run %d failed: %v", i+1, callErr)
		}

		snap := snapshotDeterministicParts(resp)

		wantOpFolders := []string{"folder1", "folder2", "folder10"}
		gotOpFolders := folderSequenceFromOps(t, resp.GetOperations())
		if !reflect.DeepEqual(gotOpFolders, wantOpFolders) {
			t.Fatalf("run %d unexpected operation folder order: got=%v want=%v", i+1, gotOpFolders, wantOpFolders)
		}

		wantSuccess := []string{
			filepath.ToSlash(filepath.Clean(folder1)),
			filepath.ToSlash(filepath.Clean(folder2)),
			filepath.ToSlash(filepath.Clean(folder10)),
		}
		if !reflect.DeepEqual(resp.GetSuccessfulFolders(), wantSuccess) {
			t.Fatalf("run %d unexpected successful_folders order: got=%v want=%v", i+1, resp.GetSuccessfulFolders(), wantSuccess)
		}

		wantErrors := []string{fmt.Sprintf("%s|%s", filepath.ToSlash(filepath.Clean(folder3)), "PATH_NULL_BYTE")}
		if !reflect.DeepEqual(snap.PlanErrors, wantErrors) {
			t.Fatalf("run %d unexpected plan_errors order/content: got=%v want=%v", i+1, snap.PlanErrors, wantErrors)
		}

		if baseline == nil {
			baseline = &snap
			continue
		}

		if !reflect.DeepEqual(*baseline, snap) {
			t.Fatalf("run %d not deterministic vs run 1: got=%+v baseline=%+v", i+1, snap, *baseline)
		}
	}
}

func TestPlanOperations_MultiFolder_GroupSortKeepsRootRelativeKey(t *testing.T) {
	tmpDir := t.TempDir()

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	scanRoot := filepath.Join(tmpDir, "scan-root")
	folder2 := filepath.Join(scanRoot, "A", "folder2")
	folder10 := filepath.Join(scanRoot, "A", "folder10")

	rootFile := filepath.Join(scanRoot, "root-track.flac")
	mustWriteFile(t, rootFile)
	insertSlimEntry(t, repo, rootFile, scanRoot, "audio/flac", 2000, nil)

	f2File := filepath.Join(folder2, "track2.flac")
	mustWriteFile(t, f2File)
	insertSlimEntry(t, repo, f2File, scanRoot, "audio/flac", 2000, nil)

	f10File := filepath.Join(folder10, "track10.flac")
	mustWriteFile(t, f10File)
	insertSlimEntry(t, repo, f10File, scanRoot, "audio/flac", 2000, nil)

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:    "slim",
		FolderPaths: []string{folder10, scanRoot, folder2},
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	if len(resp.GetPlanErrors()) > 0 {
		t.Fatalf("expected no plan_errors, got %+v", resp.GetPlanErrors())
	}

	got := folderSequenceFromOps(t, resp.GetOperations())
	gotUnique := uniquePreservingOrder(got)
	want := []string{"scan-root", "folder2", "folder10"}
	if !reflect.DeepEqual(gotUnique, want) {
		t.Fatalf("unexpected folder group order from operations: got=%v unique=%v want=%v", got, gotUnique, want)
	}

}

func TestPlanOperations_MultiFolder_GroupSortTieBreaksByCanonicalPath(t *testing.T) {
	tmpDir := t.TempDir()

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	folderZ := filepath.Join(tmpDir, "z-folder")
	folderA := filepath.Join(tmpDir, "a-folder")

	fileZ := filepath.Join(folderZ, "same-track.flac")
	mustWriteFile(t, fileZ)
	insertSlimEntry(t, repo, fileZ, folderZ, "audio/flac", 2000, nil)

	fileA := filepath.Join(folderA, "same-track.flac")
	mustWriteFile(t, fileA)
	insertSlimEntry(t, repo, fileA, folderA, "audio/flac", 2000, nil)

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	resp, err := server.PlanOperations(nil, &pb.PlanOperationsRequest{
		PlanType:    "slim",
		FolderPaths: []string{folderZ, folderA},
	})
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	if len(resp.GetPlanErrors()) > 0 {
		t.Fatalf("expected no plan_errors, got %+v", resp.GetPlanErrors())
	}

	got := folderSequenceFromOps(t, resp.GetOperations())
	want := []string{"a-folder", "z-folder"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected folder group order from operations: got=%v want=%v", got, want)
	}

	wantSuccess := []string{
		filepath.ToSlash(filepath.Clean(folderA)),
		filepath.ToSlash(filepath.Clean(folderZ)),
	}
	if !reflect.DeepEqual(resp.GetSuccessfulFolders(), wantSuccess) {
		t.Fatalf("unexpected successful_folders order: got=%v want=%v", resp.GetSuccessfulFolders(), wantSuccess)
	}
}

func TestPlanOperations_MultiFolder_InGroupRootPathHandledAsRelative(t *testing.T) {
	folderPath := filepath.ToSlash(filepath.Clean(filepath.Join(t.TempDir(), "scan-root", "A", "folder2")))
	rootPath := folderPath
	nested2 := folderPath + "/disc2/track2.flac"
	nested10 := folderPath + "/disc10/track10.flac"

	ops := []analyze.Operation{
		{Type: analyze.OpTypeDelete, SourcePath: nested10},
		{Type: analyze.OpTypeDelete, SourcePath: rootPath},
		{Type: analyze.OpTypeDelete, SourcePath: nested2},
	}

	sortOperationsDFSNatural(ops, folderPath)

	got := []string{ops[0].SourcePath, ops[1].SourcePath, ops[2].SourcePath}
	want := []string{rootPath, nested2, nested10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected in-group operation source order: got=%v want=%v", got, want)
	}

	rootSegs := operationRelativeSegments(rootPath, folderPath)
	if len(rootSegs) != 0 {
		t.Fatalf("expected root path to have empty relative segments, got=%v", rootSegs)
	}
}

func mustWriteFile(t *testing.T, fullPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", fullPath, err)
	}
	if err := os.WriteFile(fullPath, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write %q: %v", fullPath, err)
	}
}

func insertSlimEntry(t *testing.T, repo *sqlite.Repository, path, rootPath, format string, size int64, bitrate interface{}) {
	t.Helper()
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, ?, ?, 1, ?, ?)
	`, filepath.ToSlash(path), filepath.ToSlash(rootPath), size, format, 1234567890, bitrate)
	if err != nil {
		t.Fatalf("insert entry %q: %v", path, err)
	}
}

func folderSequenceFromOps(t *testing.T, ops []*pb.PlannedOperation) []string {
	t.Helper()
	got := make([]string, 0, len(ops))
	for _, op := range ops {
		source := filepath.FromSlash(op.GetSourcePath())
		got = append(got, filepath.Base(filepath.Dir(source)))
	}
	return got
}

func operationRelPathsForFolder(t *testing.T, ops []*pb.PlannedOperation, folder string) []string {
	t.Helper()
	folderPrefix := filepath.ToSlash(filepath.Clean(folder)) + "/"

	got := make([]string, 0)
	for _, op := range ops {
		source := op.GetSourcePath()
		if !strings.HasPrefix(source, folderPrefix) {
			continue
		}
		rel, err := filepath.Rel(filepath.FromSlash(folderPrefix), filepath.FromSlash(source))
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q): %v", folderPrefix, source, err)
		}
		got = append(got, filepath.ToSlash(rel))
	}
	return got
}

type determinismSnapshot struct {
	Operations       []string
	SuccessfulFolder []string
	PlanErrors       []string
}

func snapshotDeterministicParts(resp *pb.PlanOperationsResponse) determinismSnapshot {
	ops := make([]string, 0, len(resp.GetOperations()))
	for _, op := range resp.GetOperations() {
		ops = append(ops, fmt.Sprintf("%s|%s|%s", op.GetOperationType(), op.GetSourcePath(), op.GetTargetPath()))
	}

	errs := make([]string, 0, len(resp.GetPlanErrors()))
	for _, pe := range resp.GetPlanErrors() {
		errStr := fmt.Sprintf("%s|%s", pe.GetFolderPath(), pe.GetCode())
		errs = append(errs, errStr)
	}

	return determinismSnapshot{
		Operations:       ops,
		SuccessfulFolder: append([]string(nil), resp.GetSuccessfulFolders()...),
		PlanErrors:       errs,
	}
}

func uniquePreservingOrder(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}
