package workset_test

import (
	"encoding/json"
	"testing"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// resolvedMember is one member's authoritative resolution, keyed by relative
// path for legible assertions.
type resolvedMember struct {
	excluded bool
	policy   reconcile.Policy
	sources  map[string]string
}

// resolve plans the current draft and reads back the frozen per-member
// effective configuration and inheritance sources from the published revision.
// This is the same path the workbench reads, so a wrong server-side resolution
// fails these tests instead of being masked by a test-local copy.
func (f *fixture) resolve(ws *worksetusecase.WorksetView) map[string]resolvedMember {
	f.t.Helper()
	op := f.operation(ws.WorksetID)
	gen := f.runGeneration(ws.WorksetID, op.Version)
	if gen.Status != "completed" {
		f.t.Fatalf("generation failed: %+v", gen)
	}
	return f.resolvedFrom(ws, gen.RevisionID)
}

// resolvedFrom reads the frozen members of one published revision.
func (f *fixture) resolvedFrom(ws *worksetusecase.WorksetView, planID string) map[string]resolvedMember {
	f.t.Helper()
	rev, err := f.svc.GetRevision(f.ctx, ws.WorksetID, worksetusecase.OperationTypeConversion, planID)
	if err != nil {
		f.t.Fatalf("GetRevision: %v", err)
	}
	rel := map[string]string{}
	for _, m := range ws.Members {
		rel[m.MemberID] = m.RelPath
	}
	out := map[string]resolvedMember{}
	for _, m := range rev.Members {
		var policy reconcile.Policy
		if err := json.Unmarshal(m.Payload, &policy); err != nil {
			f.t.Fatalf("decode member payload: %v", err)
		}
		out[rel[m.MemberID]] = resolvedMember{
			excluded: m.Excluded,
			policy:   policy,
			sources:  m.Sources,
		}
	}
	return out
}

// TestDesignExampleInheritance walks the normative example of the design spec
// section 3.2 through save, re-read and planning.
//
//nolint:gocognit,gocyclo,cyclop,funlen // the normative spec example is one long scenario
func TestDesignExampleInheritance(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("jia", "yi", "bing")
	ws := f.createWorkset("样例", ids...)
	memberIDs := map[string]string{}
	for _, m := range ws.Members {
		memberIDs[m.RelPath] = m.MemberID
	}
	jia, yi, bing := memberIDs["jia"], memberIDs["yi"], memberIDs["bing"]

	// Common: relaxed mode, tag A, matched=WAV, unmatched=MP3. 乙 overrides
	// matched=FLAC; 丙 explicitly overrides the tags with the same common A.
	doc := draftDoc()
	doc.Mode = reconcile.ModeAvailableSources
	doc.ClassifierTags = []string{"A"}
	doc.Matched = reconcile.DesiredProfile{Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav}}
	doc.Unmatched = reconcile.DesiredProfile{
		Encoded: &reconcile.AudioOutputSpec{
			Codec:   reconcile.CodecMp3,
			Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
		},
	}
	flac := reconcile.DesiredProfile{Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecFlac}}
	doc.Members = []tasksconversion.DraftMember{
		{MemberID: yi, Overrides: &tasksconversion.OverrideSet{Matched: &flac}},
		{MemberID: bing, Overrides: &tasksconversion.OverrideSet{ClassifierTags: &[]string{"A"}}},
	}
	f.saveDraft(ws.WorksetID, doc, ws.Operations[0].Version)

	// Step 1: batch-set unmatched=WAV for 甲 and 乙. Only the unmatched unit
	// gains an override; 乙's matched=FLAC survives untouched.
	stored := f.draft(ws.WorksetID)
	storedMembers := map[string]tasksconversion.DraftMember{}
	for _, m := range mustDraft(t, stored).Members {
		storedMembers[m.MemberID] = m
	}
	wav := reconcile.DesiredProfile{Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav}}
	next := *mustDraft(t, stored)
	next.Members = nil
	for _, member := range ws.Members {
		rec := storedMembers[member.MemberID]
		rec.MemberID = member.MemberID
		if member.MemberID == jia || member.MemberID == yi {
			ov := tasksconversion.OverrideSet{}
			if rec.Overrides != nil {
				ov = *rec.Overrides
			}
			ov.Unmatched = &wav
			rec.Overrides = &ov
		}
		next.Members = append(next.Members, rec)
	}
	f.saveDraft(ws.WorksetID, &next, f.operation(ws.WorksetID).Version)

	eff := f.resolve(ws)
	if got := eff["yi"].policy.Matched; got.Lossless == nil || got.Lossless.Codec != reconcile.CodecFlac {
		t.Fatalf("step 1: 乙 matched override lost: %+v", got)
	}
	if got := eff["yi"].policy.Unmatched; got.Lossless == nil || got.Lossless.Codec != reconcile.CodecWav {
		t.Fatalf("step 1: 乙 unmatched override missing: %+v", got)
	}
	if got := eff["jia"].policy.Unmatched; got.Lossless == nil || got.Lossless.Codec != reconcile.CodecWav {
		t.Fatalf("step 1: 甲 unmatched override missing: %+v", got)
	}
	if eff["jia"].sources[tasksconversion.UnitUnmatched] != tasksconversion.SourceMember {
		t.Fatalf("step 1: 甲 unmatched source = %q", eff["jia"].sources[tasksconversion.UnitUnmatched])
	}
	if eff["jia"].sources[tasksconversion.UnitMatched] != tasksconversion.SourceCommon {
		t.Fatal("step 1: 甲 matched must stay inherited")
	}
	if eff["yi"].sources[tasksconversion.UnitMatched] != tasksconversion.SourceMember {
		t.Fatal("step 1: 乙 matched source must be member")
	}

	// Step 2: common tags change to B. 甲 and 乙 follow; 丙 keeps A because its
	// override was explicit even though the values were equal before.
	before := f.draft(ws.WorksetID)
	changed := *mustDraft(t, before)
	changed.ClassifierTags = []string{"B"}
	f.saveDraft(ws.WorksetID, &changed, f.operation(ws.WorksetID).Version)

	eff = f.resolve(ws)
	if got := eff["jia"].policy.ClassifierTags; len(got) != 1 || got[0] != "B" {
		t.Fatalf("step 2: 甲 tags = %v, want [B]", got)
	}
	if got := eff["yi"].policy.ClassifierTags; len(got) != 1 || got[0] != "B" {
		t.Fatalf("step 2: 乙 tags = %v, want [B]", got)
	}
	if got := eff["bing"].policy.ClassifierTags; len(got) != 1 || got[0] != "A" {
		t.Fatalf("step 2: 丙 tags = %v, want [A]", got)
	}
	if eff["bing"].sources[tasksconversion.UnitClassifierTags] != tasksconversion.SourceMember {
		t.Fatal("step 2: 丙 tag source must be member")
	}

	// Step 3: 丙 restores tag inheritance -> adopts B immediately, then C.
	after := f.draft(ws.WorksetID)
	restored := *mustDraft(t, after)
	restored.Members = nil
	for _, m := range mustDraft(t, after).Members {
		if m.MemberID == bing {
			continue
		}
		restored.Members = append(restored.Members, m)
	}
	f.saveDraft(ws.WorksetID, &restored, f.operation(ws.WorksetID).Version)

	eff = f.resolve(ws)
	if got := eff["bing"].policy.ClassifierTags; len(got) != 1 || got[0] != "B" {
		t.Fatalf("step 3: 丙 tags = %v, want inherited [B]", got)
	}
	if eff["bing"].sources[tasksconversion.UnitClassifierTags] != tasksconversion.SourceCommon {
		t.Fatal("step 3: 丙 tag source must be common")
	}

	third := f.draft(ws.WorksetID)
	again := *mustDraft(t, third)
	again.ClassifierTags = []string{"C"}
	f.saveDraft(ws.WorksetID, &again, f.operation(ws.WorksetID).Version)
	eff = f.resolve(ws)
	if got := eff["bing"].policy.ClassifierTags; len(got) != 1 || got[0] != "C" {
		t.Fatalf("step 3: 丙 must follow the common tags to C, got %v", got)
	}

	// Step 4: excluding 乙 keeps every override; the resolved values are still
	// available so restoring participation needs no re-editing.
	cur := f.draft(ws.WorksetID)
	excluded := *mustDraft(t, cur)
	excluded.Members = nil
	for _, m := range mustDraft(t, cur).Members {
		rec := m
		if m.MemberID == yi {
			rec.Excluded = true
		}
		excluded.Members = append(excluded.Members, rec)
	}
	f.saveDraft(ws.WorksetID, &excluded, f.operation(ws.WorksetID).Version)

	cur = f.draft(ws.WorksetID)
	for _, m := range mustDraft(t, cur).Members {
		if m.MemberID != yi {
			continue
		}
		if !m.Excluded {
			t.Fatal("step 4: 乙 must be excluded")
		}
		if m.Overrides == nil || m.Overrides.Matched == nil || m.Overrides.Unmatched == nil {
			t.Fatalf("step 4: exclusion dropped 乙's overrides: %+v", m.Overrides)
		}
	}
	eff = f.resolve(ws)
	if !eff["yi"].excluded {
		t.Fatal("step 4: 乙 exclusion not effective")
	}
	if eff["yi"].policy.Matched.Lossless == nil || eff["yi"].policy.Matched.Lossless.Codec != reconcile.CodecFlac {
		t.Fatal("step 4: excluded member must keep its resolved config")
	}
	if eff["yi"].sources[tasksconversion.UnitUnmatched] != tasksconversion.SourceMember {
		t.Fatal("step 4: excluded member must keep its override sources")
	}
}

// TestSparseOverridesSurviveRoundTrip proves unmodified units are never
// materialized: a saved document re-reads with exactly the overrides given.
func TestSparseOverridesSurviveRoundTrip(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("a", "b")
	ws := f.createWorkset("稀疏", ids...)
	var first, second string
	for _, m := range ws.Members {
		if m.RelPath == "a" {
			first = m.MemberID
		} else {
			second = m.MemberID
		}
	}
	doc := draftDoc()
	tags := []string{"only-a"}
	doc.Members = []tasksconversion.DraftMember{
		{MemberID: first, Overrides: &tasksconversion.OverrideSet{ClassifierTags: &tags}},
		// A participating member with no override is not stored at all.
		{MemberID: second},
	}
	f.saveDraft(ws.WorksetID, doc, ws.Operations[0].Version)

	stored := f.draft(ws.WorksetID)
	if len(mustDraft(t, stored).Members) != 1 || mustDraft(t, stored).Members[0].MemberID != first {
		t.Fatalf("sparse members = %+v", mustDraft(t, stored).Members)
	}
	ov := mustDraft(t, stored).Members[0].Overrides
	if ov == nil || ov.ClassifierTags == nil || ov.Mode != nil || ov.Matched != nil || ov.Unmatched != nil {
		t.Fatalf("unmodified units were materialized: %+v", ov)
	}
}

// TestExplicitEmptyTagsAreNotAbsence covers the C02 pair: "clear tags" stores
// an explicit empty array and keeps behaving differently from restored
// inheritance when the common value changes afterwards.
func TestExplicitEmptyTagsAreNotAbsence(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("clear", "restore")
	ws := f.createWorkset("清空", ids...)
	byPath := map[string]string{}
	for _, m := range ws.Members {
		byPath[m.RelPath] = m.MemberID
	}
	empty := []string{}
	doc := draftDoc()
	doc.Members = []tasksconversion.DraftMember{
		{MemberID: byPath["clear"], Overrides: &tasksconversion.OverrideSet{ClassifierTags: &empty}},
	}
	f.saveDraft(ws.WorksetID, doc, ws.Operations[0].Version)

	stored := f.draft(ws.WorksetID)
	if len(mustDraft(t, stored).Members) != 1 {
		t.Fatalf("explicit empty tags must be stored, got %+v", mustDraft(t, stored).Members)
	}
	if got := mustDraft(t, stored).Members[0].Overrides.ClassifierTags; got == nil || len(*got) != 0 {
		t.Fatalf("explicit empty tags not preserved: %+v", got)
	}
}

// TestEqualValueDifferentSource covers T04: same effective value, different
// provenance, and a common change that reaches only the inheriting member.
func TestEqualValueDifferentSource(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("inherit", "explicit")
	ws := f.createWorkset("来源", ids...)
	byPath := map[string]string{}
	for _, m := range ws.Members {
		byPath[m.RelPath] = m.MemberID
	}
	same := []string{"X"}
	doc := draftDoc()
	doc.ClassifierTags = same
	doc.Members = []tasksconversion.DraftMember{
		{MemberID: byPath["explicit"], Overrides: &tasksconversion.OverrideSet{ClassifierTags: &same}},
	}
	f.saveDraft(ws.WorksetID, doc, ws.Operations[0].Version)

	eff := f.resolve(ws)
	if eff["inherit"].sources[tasksconversion.UnitClassifierTags] != tasksconversion.SourceCommon {
		t.Fatal("inheriting member must be sourced common")
	}
	if eff["explicit"].sources[tasksconversion.UnitClassifierTags] != tasksconversion.SourceMember {
		t.Fatal("equal explicit value must stay sourced member")
	}

	stored := f.draft(ws.WorksetID)
	changed := *mustDraft(t, stored)
	changed.ClassifierTags = []string{"Y"}
	f.saveDraft(ws.WorksetID, &changed, f.operation(ws.WorksetID).Version)

	eff = f.resolve(ws)
	if got := eff["inherit"].policy.ClassifierTags; len(got) != 1 || got[0] != "Y" {
		t.Fatalf("inheriting member did not follow: %v", got)
	}
	if got := eff["explicit"].policy.ClassifierTags; len(got) != 1 || got[0] != "X" {
		t.Fatalf("explicit member changed: %v", got)
	}
}

// TestExcludingEveryMemberBlocksGeneration covers T09: all-excluded saves but
// cannot generate.
func TestExcludingEveryMemberBlocksGeneration(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("only")
	ws := f.createWorkset("全排除", ids...)
	doc := draftDoc()
	doc.Members = []tasksconversion.DraftMember{{MemberID: ws.Members[0].MemberID, Excluded: true}}
	view := f.saveDraft(ws.WorksetID, doc, ws.Operations[0].Version)

	_, err := f.svc.StartGeneration(
		f.ctx,
		ws.WorksetID,
		worksetusecase.OperationTypeConversion,
		worksetusecase.StartGenerationRequest{
			IfMatchVersion: view.Version, IdempotencyKey: "gen-all-excluded",
		},
	)
	if err == nil {
		t.Fatal("generation with every member excluded must fail")
	}
	if werr, ok := worksetusecase.AsError(err); !ok || werr.Code != "NO_ACTIVE_MEMBERS" {
		t.Fatalf("want NO_ACTIVE_MEMBERS, got %v", err)
	}
}

// TestHistoricalRevisionKeepsItsFrozenValues covers T13: after the common
// settings change, an older revision still reports the configuration and
// sources it was planned with.
func TestHistoricalRevisionKeepsItsFrozenValues(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("a")
	ws := f.createWorkset("历史", ids...)
	byPath := ws.Members[0].MemberID
	tags := []string{"old"}
	doc := draftDoc()
	doc.ClassifierTags = []string{"common-old"}
	doc.Members = []tasksconversion.DraftMember{
		{MemberID: byPath, Overrides: &tasksconversion.OverrideSet{ClassifierTags: &tags}},
	}
	f.saveDraft(ws.WorksetID, doc, ws.Operations[0].Version)

	first := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if first.Status != "completed" {
		t.Fatalf("first generation: %+v", first)
	}

	stored := f.draft(ws.WorksetID)
	changed := *mustDraft(t, stored)
	changed.ClassifierTags = []string{"common-new"}
	changed.Members = nil
	f.saveDraft(ws.WorksetID, &changed, f.operation(ws.WorksetID).Version)

	frozen := f.resolvedFrom(ws, first.RevisionID)
	if got := frozen["a"].policy.ClassifierTags; len(got) != 1 || got[0] != "old" {
		t.Fatalf("frozen revision tags = %v, want [old]", got)
	}
	if frozen["a"].sources[tasksconversion.UnitClassifierTags] != tasksconversion.SourceMember {
		t.Fatal("frozen revision must keep the override source")
	}
}
