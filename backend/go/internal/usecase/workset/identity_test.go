package workset_test

import (
	"testing"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestCreateReturnsStableMemberIdentities covers the identity contract: one
// stable member_id per folder, never regenerated from order, index or path,
// and unaffected by draft saves or metadata version bumps.
func TestCreateReturnsStableMemberIdentities(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")
	ws := f.createWorkset("身份", ids...)

	if len(ws.Members) != 2 {
		t.Fatalf("members = %+v", ws.Members)
	}
	byPath := map[string]string{}
	for _, m := range ws.Members {
		if m.MemberID == "" {
			t.Fatalf("member without identity: %+v", m)
		}
		if previous, dup := byPath[m.RelPath]; dup || previous != "" {
			t.Fatalf("duplicate rel_path %q", m.RelPath)
		}
		byPath[m.RelPath] = m.MemberID
	}

	// Draft save, rename and re-read must not change any member identity.
	f.saveDraft(ws.WorksetID, draftDoc(), ws.Operations[0].Version)
	if _, err := f.svc.RenameWorkset(f.ctx, ws.WorksetID, worksetusecase.RenameRequest{
		Title: "新名字", IfMatchVersion: ws.Version,
	}); err != nil {
		t.Fatalf("RenameWorkset: %v", err)
	}
	again, err := f.svc.GetWorkset(f.ctx, ws.WorksetID)
	if err != nil {
		t.Fatalf("GetWorkset: %v", err)
	}
	for _, m := range again.Members {
		if byPath[m.RelPath] != m.MemberID {
			t.Fatalf("member identity changed for %q: %q -> %q", m.RelPath, byPath[m.RelPath], m.MemberID)
		}
	}
}
