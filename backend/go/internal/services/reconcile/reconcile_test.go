package reconcile

import (
	"fmt"
	"strings"
	"testing"
)

// rjRoot mirrors the real directory layout at
// /mnt/d/TempDownload/unzip/RJ01567288: two content partitions (SEあり /
// SEなし) each holding codec lane directories mp3/ and wav/, with tracks
// numbered 00..05. Cross-directory pairing (mp3/00.mp3 <-> wav/00.wav) is the
// intentional component shape the classifier must not merge across partitions.
const rjRoot = "D:/Music/RJ01567288"

func rjEntry(rel string, size int64, bitrate int64) AudioEntry {
	return AudioEntry{PathPosix: rjRoot + "/" + rel, Size: size, Mtime: 1700000000, Bitrate: bitrate}
}

// rjEntries builds the standard fixture. bitrateOverride can set a custom
// bitrate for a specific mp3 (e.g. "SEなし/mp3/03.mp3" -> 128000).
func rjEntries(bitrateOverride map[string]int64) []AudioEntry {
	entries := []AudioEntry{}
	for _, partition := range []string{"SEあり", "SEなし"} {
		for i := 0; i < 6; i++ {
			file := num2(i)
			wav := rjEntry(partition+"/wav/"+file+".wav", 200000000, 0)
			mp3 := rjEntry(partition+"/mp3/"+file+".mp3", 20000000, 320000)
			if br, ok := bitrateOverride[partition+"/mp3/"+file+".mp3"]; ok {
				mp3.Bitrate = br
			}
			if br, ok := bitrateOverride[partition+"/wav/"+file+".wav"]; ok {
				wav.Bitrate = br
			}
			entries = append(entries, wav, mp3)
		}
	}
	// Bonus image folder: never audio, never grouped.
	entries = append(entries, AudioEntry{PathPosix: rjRoot + "/特典/cover.jpg", Size: 200000, Mtime: 1700000000})
	entries = append(entries, AudioEntry{PathPosix: rjRoot + "/特典/track.vtt", Size: 5000, Mtime: 1700000000})
	return entries
}

func num2(i int) string {
	return fmt.Sprintf("%02d", i)
}

func wavMp3Profile() Policy {
	return Policy{
		SchemaVersion: 1,
		Classifier:    ClassifierRef{Name: "effect-direction", Version: 1},
		Matched: DesiredProfile{
			Lossless: &AudioOutputSpec{Codec: CodecWav},
			Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: &Quality{Kind: QualityBitrate, Bitrate: 320}},
		},
		Unmatched: DesiredProfile{
			Lossless: &AudioOutputSpec{Codec: CodecWav},
			Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: &Quality{Kind: QualityBitrate, Bitrate: 320}},
		},
	}
}

func mp3OnlyProfile() Policy {
	p := wavMp3Profile()
	mp3 := DesiredProfile{Encoded: &AudioOutputSpec{Codec: CodecMp3, Quality: &Quality{Kind: QualityBitrate, Bitrate: 320}}}
	p.Matched = mp3
	p.Unmatched = mp3
	return p
}

func effectClassifier() Classifier {
	c, err := NewRegexClassifier("effect-direction", 1, `(?i)SEなし`)
	if err != nil {
		panic(err)
	}
	return c
}

func reconcileRJ(t *testing.T, entries []AudioEntry, policy Policy) ReconcileResult {
	t.Helper()
	res, err := Reconcile(ReconcileInput{
		RootPath:   rjRoot,
		Entries:    entries,
		Policy:     policy,
		Classifier: effectClassifier(),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

func TestClassifierPartitionsByRootRelativePath(t *testing.T) {
	c := effectClassifier()
	if got := c.Classify("SEなし/mp3/00.mp3"); got != PartitionMatched {
		t.Fatalf("SEなし -> %s, want matched", got)
	}
	if got := c.Classify("SEあり/wav/00.wav"); got != PartitionUnmatched {
		t.Fatalf("SEあり -> %s, want unmatched", got)
	}
	if got := c.Classify("特典/cover.jpg"); got != PartitionUnmatched {
		t.Fatalf("特典 -> %s, want unmatched", got)
	}
}

// rjAudioCount is 6 stems x 2 codec lanes x 2 partitions.
const rjAudioCount = 24

func TestReconcile_BalancedAllSatisfied_NoOperations(t *testing.T) {
	res := reconcileRJ(t, rjEntries(nil), wavMp3Profile())

	// Two partitions -> SEあり unmatched component, SEなし matched component.
	if len(res.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(res.Components))
	}
	for _, comp := range res.Components {
		if comp.Status != StatusOK {
			t.Fatalf("component %s status = %s, want ok (%s)", comp.ComponentID, comp.Status, comp.Message)
		}
		if len(comp.Operations) != 0 {
			t.Fatalf("component %s operations = %d, want 0", comp.ComponentID, len(comp.Operations))
		}
		if len(comp.Variants) != 6 {
			t.Fatalf("component %s variants = %d, want 6", comp.ComponentID, len(comp.Variants))
		}
		// Projected inventory: 6 wav + 6 mp3 = 12, no images.
		if len(comp.ProjectedInventory) != 12 {
			t.Fatalf("component %s projected = %d, want 12", comp.ComponentID, len(comp.ProjectedInventory))
		}
		for _, v := range comp.Variants {
			for _, d := range v.Decisions {
				if d.Resolution != ResolutionKeep {
					t.Fatalf("stem %s decision %s = %s, want keep", v.Stem, d.Path, d.Resolution)
				}
			}
		}
	}
	if res.Summary.SummaryReason != ReasonNoMatch {
		t.Fatalf("summary reason = %s, want NO_MATCH", res.Summary.SummaryReason)
	}
	if res.Count != rjAudioCount {
		t.Fatalf("inventory count = %d, want %d (images excluded)", res.Count, rjAudioCount)
	}
}

func TestReconcile_CompactProfile_DeletesLosslessOnly(t *testing.T) {
	res := reconcileRJ(t, rjEntries(nil), mp3OnlyProfile())

	for _, comp := range res.Components {
		if comp.Status != StatusOK {
			t.Fatalf("component %s blocked: %s", comp.ComponentID, comp.Message)
		}
		for _, op := range comp.Operations {
			if op.Kind != OpKindRemoveObsolete {
				t.Fatalf("op kind = %s, want remove_obsolete", op.Kind)
			}
			if op.Phase != PhaseRemoveObsoleteAudio {
				t.Fatalf("op phase = %s, want remove_obsolete_audio", op.Phase)
			}
		}
		// 6 wav deleted, 6 mp3 kept.
		if len(comp.Operations) != 6 {
			t.Fatalf("component %s ops = %d, want 6", comp.ComponentID, len(comp.Operations))
		}
		if len(comp.ProjectedInventory) != 6 {
			t.Fatalf("component %s projected = %d, want 6 mp3 only", comp.ComponentID, len(comp.ProjectedInventory))
		}
	}
	if res.Summary.SummaryReason != ReasonActionable {
		t.Fatalf("summary reason = %s, want ACTIONABLE", res.Summary.SummaryReason)
	}
}

func TestReconcile_RebuildAll_EncodedLane(t *testing.T) {
	// One below-target mp3 forces the whole encoded lane of its component to
	// rebuild from lossless; the partner component stays satisfied.
	entries := rjEntries(map[string]int64{"SEなし/mp3/03.mp3": 128000})
	res := reconcileRJ(t, entries, mp3OnlyProfile())

	var matched, unmatched *ComponentOutcome
	for i := range res.Components {
		if res.Components[i].Partition == PartitionMatched {
			matched = &res.Components[i]
		} else {
			unmatched = &res.Components[i]
		}
	}
	if matched == nil || unmatched == nil {
		t.Fatal("missing component partition")
	}
	if unmatched.Status != StatusOK || len(unmatched.Operations) != 6 {
		t.Fatalf("unmatched component should stay satisfied-delete-only, got status=%s ops=%d", unmatched.Status, len(unmatched.Operations))
	}
	if matched.Status != StatusOK {
		t.Fatalf("matched component blocked: %s", matched.Message)
	}
	// 6 encode ops (one per stem reusing the existing mp3 path) + 6 wav removes.
	var encodes, removes int
	for _, op := range matched.Operations {
		switch op.Kind {
		case OpKindEncode:
			encodes++
		case OpKindRemoveObsolete:
			removes++
		}
	}
	if encodes != 6 || removes != 6 {
		t.Fatalf("matched component encodes=%d removes=%d, want 6/6", encodes, removes)
	}
	// Encoded lane state must be REBUILD_ALL.
	var rebuildLane bool
	for _, lane := range matched.Lanes {
		if lane.Lane == LaneEncoded && lane.Decision == LaneRebuildAll {
			rebuildLane = true
		}
	}
	if !rebuildLane {
		t.Fatal("matched encoded lane should be REBUILD_ALL")
	}
	// Encode targets reuse existing mp3 paths (staged replacement).
	for _, op := range matched.Operations {
		if op.Kind != OpKindEncode {
			continue
		}
		if op.TargetPath == "" || op.SourcePath == "" {
			t.Fatalf("encode op missing source/target: %+v", op)
		}
	}
	// Removal depends on materialized targets.
	for _, op := range matched.Operations {
		if op.Kind != OpKindRemoveObsolete {
			continue
		}
		if len(op.DependsOn) == 0 {
			t.Fatalf("remove op %s has no depends_on", op.SourcePath)
		}
	}
}

func TestReconcile_BlockedComponent_MissingLosslessSource(t *testing.T) {
	// SEなし 03 has no wav: the whole matched component is blocked (lossless
	// lane unfulfillable). With unmatched satisfied this yields BLOCKED.
	entries := rjEntries(nil)
	for i := range entries {
		if entries[i].PathPosix == rjRoot+"/SEなし/wav/03.wav" {
			entries = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	res := reconcileRJ(t, entries, wavMp3Profile())

	var matched, unmatched *ComponentOutcome
	for i := range res.Components {
		if res.Components[i].Partition == PartitionMatched {
			matched = &res.Components[i]
		} else {
			unmatched = &res.Components[i]
		}
	}
	if matched == nil || unmatched == nil {
		t.Fatal("missing component partition")
	}
	if matched.Status != StatusBlocked {
		t.Fatalf("matched component status = %s, want blocked", matched.Status)
	}
	if matched.ReasonCode != ReasonLosslessUnfulfillable && matched.ReasonCode != ReasonSourceMissing {
		t.Fatalf("matched reason = %s", matched.ReasonCode)
	}
	if len(matched.Operations) != 0 {
		t.Fatalf("blocked component must have zero operations, got %d", len(matched.Operations))
	}
	if unmatched.Status != StatusOK || len(unmatched.Operations) != 0 {
		t.Fatalf("unmatched should stay ok/satisfied: %s ops=%d", unmatched.Status, len(unmatched.Operations))
	}
	if res.Summary.SummaryReason != ReasonBlocked {
		t.Fatalf("summary = %s, want BLOCKED (no actionable ops)", res.Summary.SummaryReason)
	}

	// When unmatched is actionable at the same time the plan is PARTIAL.
	actionable := rjEntries(map[string]int64{"SEあり/mp3/00.mp3": 128000})
	for i := range actionable {
		if actionable[i].PathPosix == rjRoot+"/SEなし/wav/03.wav" {
			actionable = append(actionable[:i], actionable[i+1:]...)
			break
		}
	}
	partial := reconcileRJ(t, actionable, wavMp3Profile())
	if partial.Summary.SummaryReason != ReasonPartial {
		t.Fatalf("summary = %s, want PARTIAL", partial.Summary.SummaryReason)
	}
	if partial.Summary.BlockedCount != 1 || partial.Summary.OperationCount == 0 {
		t.Fatalf("partial summary blocked=%d ops=%d", partial.Summary.BlockedCount, partial.Summary.OperationCount)
	}
}

func TestReconcile_SourceAmbiguous(t *testing.T) {
	// Two wav files with the same normalized stem: no unique qualified source.
	entries := []AudioEntry{
		rjEntry("SEあり/wav/00.wav", 100, 0),
		rjEntry("SEあり/wav/00.WAV", 100, 0),
		rjEntry("SEあり/mp3/00.mp3", 10, 128000),
	}
	res := reconcileRJ(t, entries, mp3OnlyProfile())

	if len(res.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(res.Components))
	}
	if res.Components[0].Status != StatusBlocked {
		t.Fatalf("status = %s, want blocked", res.Components[0].Status)
	}
	if res.Components[0].ReasonCode != ReasonSourceAmbiguous {
		t.Fatalf("reason = %s, want SOURCE_AMBIGUOUS", res.Components[0].ReasonCode)
	}
}

func TestReconcile_TargetPathAmbiguous(t *testing.T) {
	// Two mp3 files with the same stem: encode target reuse is ambiguous.
	entries := []AudioEntry{
		rjEntry("SEなし/wav/00.wav", 100, 0),
		rjEntry("SEなし/mp3/00.mp3", 10, 128000),
		rjEntry("SEなし/mp3/00.MP3", 10, 128000),
	}
	res := reconcileRJ(t, entries, mp3OnlyProfile())

	if len(res.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(res.Components))
	}
	if res.Components[0].Status != StatusBlocked {
		t.Fatalf("status = %s, want blocked", res.Components[0].Status)
	}
	if res.Components[0].ReasonCode != ReasonTargetPathAmbiguous {
		t.Fatalf("reason = %s, want TARGET_PATH_AMBIGUOUS", res.Components[0].ReasonCode)
	}
}

func TestReconcile_UnknownBitrate(t *testing.T) {
	// mp3 bitrate 0 with a lossless source -> rebuild; without a source -> blocked.
	// Remove only SEなし/wav/00.wav so the matched component has an
	// unknown-bitrate mp3 with no lossless source (SEあり stays intact).
	entries := rjEntries(map[string]int64{"SEなし/mp3/00.mp3": 0})
	for i := range entries {
		if entries[i].PathPosix == rjRoot+"/SEなし/wav/00.wav" {
			entries = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	res := reconcileRJ(t, entries, mp3OnlyProfile())
	var matched *ComponentOutcome
	var unmatched *ComponentOutcome
	for i := range res.Components {
		if res.Components[i].Partition == PartitionMatched {
			matched = &res.Components[i]
		} else {
			unmatched = &res.Components[i]
		}
	}
	if matched == nil || unmatched == nil {
		t.Fatalf("expected matched and unmatched components, got %d", len(res.Components))
	}
	if matched.Status != StatusBlocked || matched.ReasonCode != ReasonQualityUnknown {
		t.Fatalf("unknown bitrate without source should block with QUALITY_UNKNOWN, got %s %s", matched.Status, matched.ReasonCode)
	}
	if len(matched.Operations) != 0 {
		t.Fatalf("blocked matched component must have zero operations, got %d", len(matched.Operations))
	}
	if unmatched.Status != StatusOK {
		t.Fatalf("unmatched component should stay satisfied: %s %s", unmatched.Status, unmatched.Message)
	}

	withSourceRes := reconcileRJ(t, rjEntries(map[string]int64{"SEなし/mp3/00.mp3": 0}), mp3OnlyProfile())
	var matched2 *ComponentOutcome
	for i := range withSourceRes.Components {
		if withSourceRes.Components[i].Partition == PartitionMatched {
			matched2 = &withSourceRes.Components[i]
		}
	}
	if matched2 == nil || matched2.Status != StatusOK {
		t.Fatalf("unknown bitrate WITH source should rebuild, got %s %s", matched2.Status, matched2.Message)
	}
	var encodes int
	for _, op := range matched2.Operations {
		if op.Kind == OpKindEncode {
			encodes++
		}
	}
	if encodes != 6 {
		t.Fatalf("encodes = %d, want 6 (rebuild all)", encodes)
	}
}

func TestReconcile_LosslessRebuildToFlac(t *testing.T) {
	// Target flac: wav is converted lossless->lossless and the wav becomes
	// obsolete after the flac commits.
	policy := wavMp3Profile()
	policy.Matched = DesiredProfile{
		Lossless: &AudioOutputSpec{Codec: CodecFlac},
		Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: &Quality{Kind: QualityBitrate, Bitrate: 320}},
	}
	policy.Unmatched = DesiredProfile{
		Lossless: &AudioOutputSpec{Codec: CodecFlac},
		Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: &Quality{Kind: QualityBitrate, Bitrate: 320}},
	}
	res := reconcileRJ(t, rjEntries(nil), policy)

	for _, comp := range res.Components {
		if comp.Status != StatusOK {
			t.Fatalf("component blocked: %s", comp.Message)
		}
		var flacEncode, wavRemove bool
		for _, op := range comp.Operations {
			if op.Kind == OpKindEncode && strings.HasSuffix(op.TargetPath, "/wav/00.flac") {
				flacEncode = true
			}
			if op.Kind == OpKindRemoveObsolete && strings.HasSuffix(op.SourcePath, "/wav/00.wav") {
				wavRemove = true
			}
		}
		if !flacEncode || !wavRemove {
			t.Fatalf("missing flac encode or wav remove ops: %+v", comp.Operations)
		}
	}
}

func TestReconcile_AacTargetUsesM4aContainer(t *testing.T) {
	policy := wavMp3Profile()
	target := &AudioOutputSpec{Codec: CodecAac, Quality: &Quality{Kind: QualityBitrate, Bitrate: 256}}
	policy.Matched = DesiredProfile{Encoded: target}
	policy.Unmatched = DesiredProfile{Encoded: target}
	entries := []AudioEntry{
		rjEntry("SEあり/wav/00.wav", 100, 0),
		rjEntry("SEあり/mp3/00.mp3", 20, 192000),
	}
	res := reconcileRJ(t, entries, policy)

	if len(res.Components) != 1 || res.Components[0].Status != StatusOK {
		t.Fatalf("aac profile should rebuild: %+v", res.Components)
	}
	var m4aEncode bool
	for _, op := range res.Components[0].Operations {
		if op.Kind == OpKindEncode && op.TargetPath == rjRoot+"/SEあり/wav/00.m4a" {
			m4aEncode = true
		}
	}
	if !m4aEncode {
		t.Fatalf("missing m4a encode target: %+v", res.Components[0].Operations)
	}
}

func TestReconcile_DigestDeterministicAndSensitive(t *testing.T) {
	a := rjEntries(nil)
	b := make([]AudioEntry, len(a))
	copy(b, a)
	// Shuffle order: digest must be order-invariant.
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	d1, c1 := InventoryFingerprint(a)
	d2, c2 := InventoryFingerprint(b)
	if d1 != d2 || c1 != c2 {
		t.Fatalf("fingerprint order-dependent: %q/%d vs %q/%d", d1, c1, d2, c2)
	}

	changed := rjEntries(map[string]int64{})
	changed[0].Size++
	d3, _ := InventoryFingerprint(changed)
	if d3 == d1 {
		t.Fatal("fingerprint must change on size delta")
	}

	changed2 := rjEntries(nil)
	changed2[1].Mtime++
	d4, _ := InventoryFingerprint(changed2)
	if d4 == d1 {
		t.Fatal("fingerprint must change on mtime delta")
	}

	extra := append(rjEntries(nil), rjEntry("SEあり/mp3/00 extra.mp3", 5, 320000))
	d5, c5 := InventoryFingerprint(extra)
	if d5 == d1 || c5 != c1+1 {
		t.Fatalf("fingerprint must change on addition (count %d -> %d)", c1, c5)
	}
}

func TestComponentID_StableAcrossShuffledInput(t *testing.T) {
	entries := rjEntries(nil) // includes both partitions
	res1 := reconcileRJ(t, entries, wavMp3Profile())

	shuffled := make([]AudioEntry, len(entries))
	copy(shuffled, entries)
	for i, j := 0, len(shuffled)-1; i < j; i, j = i+1, j-1 {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	res2 := reconcileRJ(t, shuffled, wavMp3Profile())

	if len(res1.Components) != len(res2.Components) {
		t.Fatalf("component count differs: %d vs %d", len(res1.Components), len(res2.Components))
	}
	ids := map[string]bool{}
	for i := range res1.Components {
		id1 := res1.Components[i].ComponentID
		if ids[id1] {
			t.Fatalf("duplicate component id %s", id1)
		}
		ids[id1] = true
		found := false
		for j := range res2.Components {
			if res2.Components[j].ComponentID == id1 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("component id %s missing after shuffle", id1)
		}
	}
}

func TestReconcile_NonAudioNeverEntersPlan(t *testing.T) {
	res := reconcileRJ(t, rjEntries(nil), mp3OnlyProfile())
	for _, comp := range res.Components {
		for _, op := range comp.Operations {
			if op.SourcePath == rjRoot+"/特典/cover.jpg" || op.SourcePath == rjRoot+"/特典/track.vtt" {
				t.Fatalf("non-audio leaked into operations: %s", op.SourcePath)
			}
		}
		for _, p := range comp.ProjectedInventory {
			if p == rjRoot+"/特典/cover.jpg" {
				t.Fatalf("non-audio leaked into projected inventory")
			}
		}
	}
}

func TestReconcile_RebuildReusesExistingTargetPath(t *testing.T) {
	// One below-target mp3 with a wav source: the encode target reuses the
	// existing mp3 path (staged replacement), never a fresh sibling.
	entries := rjEntries(map[string]int64{"SEあり/mp3/01.mp3": 128000})
	res := reconcileRJ(t, entries, wavMp3Profile())

	var comp *ComponentOutcome
	for i := range res.Components {
		if res.Components[i].Partition == PartitionUnmatched {
			comp = &res.Components[i]
		}
	}
	if comp == nil {
		t.Fatal("missing unmatched component")
	}
	if comp.Status != StatusOK {
		t.Fatalf("component blocked: %s", comp.Message)
	}
	found := false
	for _, op := range comp.Operations {
		if op.Kind == OpKindEncode && op.VariantStem == "01" {
			if op.TargetPath != rjRoot+"/SEあり/mp3/01.mp3" {
				t.Fatalf("encode target = %q, want existing mp3 path", op.TargetPath)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing encode op for stem 01")
	}
}

func TestReconcile_EncodedSourcePrefersWavOverFlac(t *testing.T) {
	// Both flac and wav exist; a below-target mp3 must encode from the WAV
	// (observed desired-lossless is absent in this mp3-only-encoded profile,
	// then WAV wins over FLAC).
	policy := mp3OnlyProfile()
	entries := []AudioEntry{
		rjEntry("SEなし/wav/00.wav", 100, 0),
		rjEntry("SEなし/flac/00.flac", 80, 0),
		rjEntry("SEなし/mp3/00.mp3", 20, 128000),
	}
	res := reconcileRJ(t, entries, policy)
	if len(res.Components) != 1 || res.Components[0].Status != StatusOK {
		t.Fatalf("expected one actionable component: %+v", res.Components)
	}
	for _, op := range res.Components[0].Operations {
		if op.Kind == OpKindEncode {
			if op.SourcePath != rjRoot+"/SEなし/wav/00.wav" {
				t.Fatalf("encode source = %q, want wav preferred over flac", op.SourcePath)
			}
		}
	}
}

func TestReconcile_LosslessToFlacDeletesOnlyNonTargetLossless(t *testing.T) {
	// Target flac: the wav is converted lossless->lossless and removed; the
	// existing flac is kept, and the below-target mp3 is rebuilt from the flac
	// (desired lossless exists).
	policy := wavMp3Profile()
	policy.Matched = DesiredProfile{
		Lossless: &AudioOutputSpec{Codec: CodecFlac},
		Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: &Quality{Kind: QualityBitrate, Bitrate: 320}},
	}
	policy.Unmatched = DesiredProfile{
		Lossless: &AudioOutputSpec{Codec: CodecFlac},
		Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: &Quality{Kind: QualityBitrate, Bitrate: 320}},
	}
	entries := []AudioEntry{
		rjEntry("SEあり/wav/00.wav", 100, 0),
		rjEntry("SEあり/flac/00.flac", 80, 0),
		rjEntry("SEあり/mp3/00.mp3", 20, 128000),
	}
	res := reconcileRJ(t, entries, policy)
	if len(res.Components) != 1 || res.Components[0].Status != StatusOK {
		t.Fatalf("expected one actionable component: %+v", res.Components)
	}
	// The existing flac satisfies the lossless target (no wav->flac encode);
	// the wav is obsolete, and the below-target mp3 is rebuilt from the
	// desired flac source.
	var flacKept, wavRemove, mp3Rebuild bool
	for _, op := range res.Components[0].Operations {
		switch {
		case op.Kind == OpKindRemoveObsolete && op.SourcePath == rjRoot+"/SEあり/wav/00.wav":
			wavRemove = true
		case op.Kind == OpKindEncode && op.TargetPath == rjRoot+"/SEあり/mp3/00.mp3":
			mp3Rebuild = true
			if op.SourcePath != rjRoot+"/SEあり/flac/00.flac" {
				t.Fatalf("mp3 rebuild source = %q, want desired flac", op.SourcePath)
			}
		}
	}
	for _, v := range res.Components[0].Variants {
		for _, d := range v.Decisions {
			if d.Path == rjRoot+"/SEあり/flac/00.flac" && d.Resolution == ResolutionKeep {
				flacKept = true
			}
		}
	}
	if !flacKept || !wavRemove || !mp3Rebuild {
		t.Fatalf("missing flac keep=%v wav remove=%v mp3 rebuild=%v: %+v", flacKept, wavRemove, mp3Rebuild, res.Components[0].Operations)
	}
}

func TestReconcile_AacUnverifiableQualityRebuildsOrBlocks(t *testing.T) {
	// AAC/M4A quality cannot be probed in v1: an aac target never keeps an
	// unverifiable aac file as satisfied. With a lossless source the lane
	// rebuilds; without one the component blocks.
	policy := wavMp3Profile()
	target := &AudioOutputSpec{Codec: CodecAac, Quality: &Quality{Kind: QualityBitrate, Bitrate: 128}}
	policy.Matched = DesiredProfile{Encoded: target}
	policy.Unmatched = DesiredProfile{Encoded: target}

	// With source: REBUILD_ALL to <source dir>/00.m4a.
	withSource := []AudioEntry{
		rjEntry("SEあり/wav/00.wav", 100, 0),
		rjEntry("SEあり/m4a/00.m4a", 20, 0),
	}
	res := reconcileRJ(t, withSource, policy)
	if len(res.Components) != 1 || res.Components[0].Status != StatusOK {
		t.Fatalf("aac with source should rebuild: %+v", res.Components)
	}
	// The existing m4a path is reused as the staged replacement target.
	var m4aEncode bool
	for _, op := range res.Components[0].Operations {
		if op.Kind == OpKindEncode && op.TargetPath == rjRoot+"/SEあり/m4a/00.m4a" {
			m4aEncode = true
		}
	}
	if !m4aEncode {
		t.Fatalf("missing aac encode target: %+v", res.Components[0].Operations)
	}

	// Without source: blocked, zero operations.
	noSource := []AudioEntry{
		rjEntry("SEなし/m4a/00.m4a", 20, 0),
	}
	blocked := reconcileRJ(t, noSource, policy)
	if len(blocked.Components) != 1 || blocked.Components[0].Status != StatusBlocked {
		t.Fatalf("aac without source should block: %+v", blocked.Components)
	}
	if len(blocked.Components[0].Operations) != 0 {
		t.Fatalf("blocked component must have zero operations")
	}
}
