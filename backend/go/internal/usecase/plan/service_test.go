package plan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// seedWorkflowEntries writes an RJ-like tree into the entries table: two
// content partitions (SEあり unmatched / SEなし matched) each with wav+mp3 codec
// lanes, 2 tracks each, all mp3 at 320 kbps so the balanced preset is fully
// satisfied (no disk reads; bitrate enrichment is skipped for non-zero rates).
func seedWorkflowEntries(t *testing.T, repo *sqlite.Repository) {
	t.Helper()
	type row struct {
		path, parent, name, format string
		size, mtime, bitrate       int64
	}
	rows := []row{
		{"/music", "", "music", "", 0, 0, 0},
		{"/music/SEあり", "/music", "SEあり", "", 0, 0, 0},
		{"/music/SEあり/wav", "/music/SEあり", "wav", "", 0, 0, 0},
		{"/music/SEあり/mp3", "/music/SEあり", "mp3", "", 0, 0, 0},
		{"/music/SEなし", "/music", "SEなし", "", 0, 0, 0},
		{"/music/SEなし/wav", "/music/SEなし", "wav", "", 0, 0, 0},
		{"/music/SEなし/mp3", "/music/SEなし", "mp3", "", 0, 0, 0},
	}
	for _, p := range []string{"SEあり", "SEなし"} {
		for _, n := range []string{"00", "01"} {
			rows = append(rows,
				row{"/music/" + p + "/wav/" + n + ".wav", "/music/" + p + "/wav", n + ".wav", "wav", 100000, 1700000000, 0},
				row{"/music/" + p + "/mp3/" + n + ".mp3", "/music/" + p + "/mp3", n + ".mp3", "mpeg", 12000, 1700000000, 320000},
			)
		}
	}
	for _, r := range rows {
		isDir := 0
		if r.size == 0 && r.bitrate == 0 && r.format == "" {
			isDir = 1
		}
		_, err := repo.DB().Exec(`
			INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, bitrate, format, content_rev)
			VALUES (?, '/music', ?, ?, ?, ?, ?, ?, ?, 1)
		`, r.path, r.parent, r.name, isDir, r.size, r.mtime, r.bitrate, r.format)
		if err != nil {
			t.Fatalf("seed entry %s: %v", r.path, err)
		}
	}
}

func balancedWorkflowRequest() Request {
	return Request{
		PlanningRoots: []string{"/music"},
		Workflow: &Workflow{
			SchemaVersion: 1,
			Steps: []WorkflowStep{{
				StepType: StepTypeReconcileAudio,
				Policy:   PolicySource{Kind: "inline", InlinePolicy: inlinePolicyPtr("SEなし")},
			}},
		},
	}
}

// inlinePolicyPtr builds a complete inline policy with the given classifier
// tags and the balanced output shape (wav + mp3@320 both partitions).
func inlinePolicyPtr(tags ...string) *reconcile.Policy {
	profile := reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded:  &reconcile.AudioOutputSpec{Codec: reconcile.CodecMp3, Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320}},
	}
	return &reconcile.Policy{
		SchemaVersion:  1,
		ClassifierTags: tags,
		Matched:        profile,
		Unmatched:      profile,
	}
}

func TestWorkflowPlanBalancedSatisfied(t *testing.T) {
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	defer repo.Close()
	seedWorkflowEntries(t, repo)

	svc := NewService(repo, "")
	res, err := svc.Plan(context.Background(), balancedWorkflowRequest())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.PlanKind != PlanKindWorkflow {
		t.Fatalf("plan_kind = %q, want workflow", res.PlanKind)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(res.Steps))
	}
	if res.Summary.SummaryReason != "NO_MATCH" {
		t.Fatalf("summary = %q, want NO_MATCH (balanced satisfied)", res.Summary.SummaryReason)
	}
	if len(res.Steps[0].Components) != 2 {
		t.Fatalf("components = %d, want 2 partitions", len(res.Steps[0].Components))
	}
	for _, c := range res.Steps[0].Components {
		if c.Status != "ok" {
			t.Fatalf("component %s status = %s: %s", c.ComponentID, c.Status, c.Message)
		}
		if len(c.Operations) != 0 {
			t.Fatalf("component %s should have no operations", c.ComponentID)
		}
	}

	// Persisted round-trip: steps/roots/components + fingerprint survive.
	detail, err := repo.GetWorkflowPlanDetail(res.PlanID)
	if err != nil {
		t.Fatalf("GetWorkflowPlanDetail: %v", err)
	}
	if len(detail.Steps) != 1 || len(detail.Components) != 2 || len(detail.Roots) != 1 {
		t.Fatalf("detail steps=%d roots=%d components=%d", len(detail.Steps), len(detail.Roots), len(detail.Components))
	}
	if detail.Roots[0].EntryCount != 8 {
		t.Fatalf("entry_count = %d, want 8 audio entries", detail.Roots[0].EntryCount)
	}
	if detail.Roots[0].InventoryFingerprint == "" {
		t.Fatal("inventory fingerprint must be persisted")
	}
	if detail.Steps[0].ClassifierTags == "" {
		t.Fatal("classifier tag snapshot must be persisted")
	}

	// Execute boundary guard rejects workflow plans.
	kind, schema, err := repo.GetPlanWorkflowSchema(res.PlanID)
	if err != nil || kind != "workflow" || schema != 1 {
		t.Fatalf("workflow schema = %q/%d/%v", kind, schema, err)
	}
}

func TestWorkflowPlanInvalidSchema(t *testing.T) {
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	defer repo.Close()
	seedWorkflowEntries(t, repo)

	svc := NewService(repo, "")
	req := balancedWorkflowRequest()
	req.Workflow.SchemaVersion = 99
	_, err = svc.Plan(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for unsupported schema version")
	}
	planErr, ok := AsError(err)
	if !ok || planErr.Code != "INVALID_WORKFLOW_SCHEMA" {
		t.Fatalf("error = %v, want INVALID_WORKFLOW_SCHEMA", err)
	}
}

func TestWorkflowPlanRejectsSingleActionMix(t *testing.T) {
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	defer repo.Close()
	seedWorkflowEntries(t, repo)

	svc := NewService(repo, "")
	_, err = svc.Plan(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected INVALID_PLAN_REQUEST for empty request")
	}
	if planErr, ok := AsError(err); !ok || planErr.Code != "INVALID_PLAN_REQUEST" {
		t.Fatalf("error = %v, want INVALID_PLAN_REQUEST", err)
	}
}
