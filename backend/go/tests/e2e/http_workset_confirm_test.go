package e2e //nolint:testpackage // shares the backend-binary e2e harness

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestHTTPWorksetOperationDraftAndConfirm drives the operation main acceptance
// chain over the real backend HTTP API: create a workset, save a sparse draft
// carrying an exclusion and a member override on an available_sources common
// configuration, generate, wait for the revision, verify the excluded member
// produced no planning root and that the frozen review reports per-unit
// inheritance sources, then confirm the revision and check idempotency.
//
//nolint:funlen // e2e scenario
func TestHTTPWorksetOperationDraftAndConfirm(t *testing.T) {
	binPath := buildBackendBinary(t)
	dataDir := t.TempDir()
	rootPath := filepath.Join(dataDir, "music")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test1.mp3"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test1.flac"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumB", "track.wav"), "dummy audio")

	const token = "e2e-token"
	proc := startBackendBinary(t, binPath, dataDir, token)
	base := fmt.Sprintf("http://127.0.0.1:%d", proc.httpPort)
	client := &http.Client{Timeout: 120 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	libReq := map[string]string{"name": "E2E Confirm", "root_path": filepath.ToSlash(rootPath)}
	var lib struct{ ID string }
	if code := doJSON(
		t, client, ctx, base, http.MethodPost, "/api/v1/libraries", token, libReq, &lib,
	); code != http.StatusCreated {
		t.Fatalf("create lib: %d", code)
	}
	events := postScanSSE(t, client, ctx, base, lib.ID, token)
	if events["completed"] == nil {
		t.Fatal("scan SSE missing completed")
	}

	var folders struct {
		Folders []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"folders"`
	}
	if code := doJSON(
		t, client, ctx, base, http.MethodGet, "/api/v1/libraries/"+lib.ID+"/folders", token, nil, &folders,
	); code != http.StatusOK || len(folders.Folders) < 2 {
		t.Fatalf("folders: code=%d n=%d", code, len(folders.Folders))
	}
	folderID := map[string]string{}
	for _, f := range folders.Folders {
		folderID[filepath.Base(filepath.FromSlash(f.Path))] = f.ID // folder identity
	}

	folderIDs := []string{folderID["albumA"], folderID["albumB"]}
	wsReq := map[string]any{"library_id": lib.ID, "title": "確認批次", "folder_ids": folderIDs}
	var wsResp struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
			Version   int    `json:"version"`
			Members   []struct {
				MemberID string `json:"member_id"`
				RelPath  string `json:"rel_path"`
			} `json:"members"`
			Operations []struct {
				OperationType string `json:"operation_type"`
				Version       int    `json:"version"`
			} `json:"operations"`
		} `json:"workset"`
	}
	if code := doJSON(
		t, client, ctx, base, http.MethodPost, "/api/v1/worksets", token, wsReq, &wsResp,
	); code != http.StatusCreated {
		t.Fatalf("create workset: %d", code)
	}
	wsID := wsResp.Workset.WorksetID
	wsPath := "/api/v1/worksets/" + wsID
	opPath := wsPath + "/operations/conversion"
	members := wsResp.Workset.Members
	if len(members) != 2 || members[0].MemberID == "" {
		t.Fatalf("members missing member_id: %+v", members)
	}
	if len(wsResp.Workset.Operations) != 1 || wsResp.Workset.Operations[0].OperationType != "conversion" {
		t.Fatalf("conversion operation not established with the workset: %+v", wsResp.Workset.Operations)
	}
	albumA, albumB := members[0].MemberID, members[1].MemberID

	// Fetch the draft: the seeded document is sparse (no member records).
	var draft struct {
		Version  int `json:"version"`
		Document struct {
			Mode    string `json:"mode"`
			Members []struct {
				MemberID string `json:"member_id"`
			} `json:"members"`
		} `json:"document"`
	}
	if code := doJSON(
		t, client, ctx, base, http.MethodGet, opPath+"/draft", token, nil, &draft,
	); code != http.StatusOK {
		t.Fatalf("get draft: %d", code)
	}
	if draft.Document.Mode != "available_sources" || len(draft.Document.Members) != 0 {
		t.Fatalf("seeded draft: %+v", draft.Document)
	}

	// Sparse draft: relaxed common mode with an encoded-only target (the wav-only
	// albumB member has no qualified source), member albumA overrides the
	// matched unit, and albumB is excluded from this operation.
	mp3Target := map[string]any{"codec": "mp3", "quality": map[string]any{"kind": "bitrate", "bitrate": 320}}
	losslessOverride := map[string]any{"lossless": map[string]any{"codec": "wav"}, "encoded": mp3Target}
	doc := map[string]any{
		"schema_version":  1,
		"mode":            "available_sources",
		"classifier_tags": []string{"SEなし"},
		"matched":         map[string]any{"encoded": mp3Target},
		"unmatched":       map[string]any{"encoded": mp3Target},
		"members": []any{
			map[string]any{"member_id": albumA, "overrides": map[string]any{"matched": losslessOverride}},
			map[string]any{"member_id": albumB, "excluded": true},
		},
	}
	var saved struct{ Version int }
	if code := doJSONWithHeaders(
		t, client, ctx, base, http.MethodPut,
		opPath+"/draft", token,
		map[string]string{"If-Match": strconv.Itoa(draft.Version)},
		doc, &saved,
	); code != http.StatusOK {
		t.Fatalf("save sparse draft: %d", code)
	}
	if saved.Version != draft.Version+1 {
		t.Fatalf("draft save must advance the operation version: %d -> %d", draft.Version, saved.Version)
	}

	// Generate on the new version; If-Match and Idempotency-Key are both required.
	if code := doJSONWithHeaders(
		t, client, ctx, base, http.MethodPost,
		opPath+"/revisions", token,
		map[string]string{"Idempotency-Key": "confirm-gen-1", "If-Match": strconv.Itoa(saved.Version)},
		nil, nil,
	); code != http.StatusAccepted {
		t.Fatalf("start generation: %d", code)
	}
	revisionID := waitForLatestRevision(t, client, ctx, base, token, wsPath, opPath)

	// The excluded member is in the frozen scope but contributes no planning
	// root, and the override unit reports its member provenance.
	var rev struct {
		Confirmation struct {
			Confirmed bool `json:"confirmed"`
		} `json:"confirmation"`
		Members []struct {
			MemberID string            `json:"member_id"`
			Excluded bool              `json:"excluded"`
			Sources  map[string]string `json:"sources"`
		} `json:"members"`
		Roots []struct {
			RootPath string `json:"root_path"`
		} `json:"roots"`
	}
	if code := doJSON(
		t, client, ctx, base, http.MethodGet, opPath+"/revisions/"+revisionID, token, nil, &rev,
	); code != http.StatusOK {
		t.Fatalf("revision detail: %d", code)
	}
	if rev.Confirmation.Confirmed {
		t.Fatal("fresh revision must not be pre-confirmed")
	}
	for _, r := range rev.Roots {
		if filepath.Base(filepath.FromSlash(r.RootPath)) == "albumB" {
			t.Fatalf("excluded member planned anyway: %+v", rev.Roots)
		}
	}
	if len(rev.Members) != 2 {
		t.Fatalf("frozen revision members = %+v", rev.Members)
	}
	for _, m := range rev.Members {
		switch m.MemberID {
		case albumA:
			if m.Sources["matched"] != "member" || m.Sources["classifier_tags"] != "common" {
				t.Fatalf("albumA sources = %+v", m.Sources)
			}
		case albumB:
			if !m.Excluded {
				t.Fatalf("albumB must be frozen as excluded: %+v", m)
			}
		default:
			t.Fatalf("unexpected member in frozen revision: %+v", m)
		}
	}

	// Confirmation is guarded by the operation version, which the publish bumped.
	confirmedVersion := saved.Version + 1
	code := doJSONWithHeaders(
		t, client, ctx, base, http.MethodPost,
		opPath+"/revisions/"+revisionID+"/confirmation", token,
		map[string]string{"If-Match": strconv.Itoa(confirmedVersion)},
		map[string]any{}, nil,
	)
	if code != http.StatusCreated {
		t.Fatalf("first confirm: %d", code)
	}
	code = doJSONWithHeaders(
		t, client, ctx, base, http.MethodPost,
		opPath+"/revisions/"+revisionID+"/confirmation", token,
		map[string]string{"If-Match": strconv.Itoa(confirmedVersion)},
		map[string]any{}, nil,
	)
	if code != http.StatusOK {
		t.Fatalf("repeat confirm: %d", code)
	}
}

// waitForLatestRevision polls the operation detail until the newest generation
// completes and returns its revision id.
func waitForLatestRevision(
	t *testing.T,
	client *http.Client,
	ctx context.Context,
	base, token, wsPath, opPath string,
) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var wsDetail struct {
			Operations []struct {
				LatestGeneration *struct {
					GenerationID string `json:"generation_id"`
				} `json:"latest_generation"`
			} `json:"operations"`
		}
		_ = doJSON(t, client, ctx, base, http.MethodGet, wsPath, token, nil, &wsDetail)
		if len(wsDetail.Operations) == 1 && wsDetail.Operations[0].LatestGeneration != nil {
			var genDetail struct {
				Status     string `json:"status"`
				RevisionID string `json:"revision_id"`
				ErrorCode  string `json:"error_code"`
			}
			if code := doJSON(
				t, client, ctx, base, http.MethodGet,
				opPath+"/planning-sessions/"+wsDetail.Operations[0].LatestGeneration.GenerationID,
				token, nil, &genDetail,
			); code == http.StatusOK {
				if genDetail.Status == "completed" {
					return genDetail.RevisionID
				}
				if genDetail.Status == "failed" || genDetail.Status == "canceled" {
					t.Fatalf("generation terminal: %+v", genDetail)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("generation not completed in time")
		}
		time.Sleep(300 * time.Millisecond)
	}
}
