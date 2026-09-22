package reconcile

import "regexp"

// Core domain types for the reconcile_audio_outputs planning pass.
//
// The pipeline is: Observed Inventory -> classifier partition -> Component
// discovery -> Variant Groups -> Desired Audio Profile -> lane reconciliation
// -> Decisions -> Operations -> Projected Audio Inventory. This package is
// deliberately DB-free: callers (the plan usecase) collect entries, enrich
// bitrate facts, and resolve the policy/classifier before invoking Reconcile.

// Partition is the classifier-derived content class. "matched" is the
// classifier match (UI: 无音效), "unmatched" is its complement (UI: 有音效).
type Partition string

const (
	PartitionMatched   Partition = "matched"
	PartitionUnmatched Partition = "unmatched"
)

// Codec is the modeled audio codec family, derived from the file extension.
type Codec string

const (
	CodecWav  Codec = "wav"
	CodecFlac Codec = "flac"
	CodecMp3  Codec = "mp3"
	CodecAac  Codec = "aac" // .aac or .m4a container
	CodecOpus Codec = "opus"
)

// ExtForCodec returns the canonical container extension for a target codec.
func ExtForCodec(c Codec) string {
	switch c {
	case CodecWav:
		return ".wav"
	case CodecFlac:
		return ".flac"
	case CodecMp3:
		return ".mp3"
	case CodecAac:
		return ".m4a"
	case CodecOpus:
		return ".opus"
	}
	return ""
}

// IsLosslessCodec reports whether the codec is a lossless source class.
func IsLosslessCodec(c Codec) bool {
	return c == CodecWav || c == CodecFlac
}

// Rate-control modes of an encoded target (the record's own vocabulary).
const (
	ModeCBR = "cbr"
	ModeVBR = "vbr"
)

// EncodingMode names the rate-control mode this app writes for an encoded
// codec. It is part of the target's identity: a file generated under another
// mode is not evidence for this one.
func EncodingMode(c Codec) string {
	if c == CodecOpus {
		return ModeVBR
	}
	return ModeCBR
}

// GeneratedFacts is one file's generation credential: the target this app
// generated it for, and the facts that bind the record to those exact bytes.
// It is present only for an output this app wrote, verified (stream and full
// decode) and committed — a file generated elsewhere carries none, and a file
// that changed since its generation no longer matches its own record.
//
// It is the second entry of the encoded-lane gate, for what a measurement
// cannot settle: a file it accepts is proven to have been produced by the
// declared configuration, which is not a claim about how it sounds — a
// low-bitrate source encoded to a high target still holds the audio it started
// with.
type GeneratedFacts struct {
	Codec       Codec
	BitrateKbps int
	Mode        string
	Size        int64
	Mtime       int64
}

// Matches reports whether the credential proves this exact file was generated
// for the given target. Size and mtime are the entry's observed facts: they are
// the same content proxy the scanner and the fingerprint use, so a rewrite that
// preserves both is the one blind spot this shares with them.
func (g *GeneratedFacts) Matches(spec AudioOutputSpec, size, mtime int64) bool {
	switch {
	case g == nil:
		return false // generated elsewhere: unconfirmed, never assumed adequate
	case g.Size != size || g.Mtime != mtime:
		return false // the file on disk is no longer the one that was generated
	case g.Codec != spec.Codec:
		return false
	case spec.Quality == nil || spec.Quality.Kind != QualityBitrate:
		return false
	default:
		return g.BitrateKbps == spec.Quality.Bitrate && g.Mode == EncodingMode(spec.Codec)
	}
}

// AudioEntry is an observed recognized-audio file. Size and Mtime feed the
// metadata inventory fingerprint; Bitrate is the enriched fact the gate reads
// first, and Generated is the generation credential that settles what the
// bitrate cannot (VBR averages, a saturating encoder).
type AudioEntry struct {
	PathPosix string
	Size      int64
	Mtime     int64
	Bitrate   int64
	Format    string
	Generated *GeneratedFacts
}

// FileTuple is the (path, size, mtime) snapshot persisted per component for
// future Execute revalidation. It never contains audio bytes.
type FileTuple struct {
	Path  string
	Size  int64
	Mtime int64
}

// GroupedFile is an AudioEntry with derived grouping facts.
type GroupedFile struct {
	AudioEntry

	ParentPath string
	Stem       string
	Ext        string
	Codec      Codec
	Lossless   bool
}

// Component is a connected set of audio files discovered by the intentional
// same-parent OR same-stem transitive relation. It is the encoded-lane
// consistency and fail-closed boundary of the audio step.
type Component struct {
	Files []GroupedFile
}

// StemGroup is one logical track's media variants within a Component.
type StemGroup struct {
	Stem  string
	Files []GroupedFile
}

// Classifier is the resolved literal-tag classifier: the normalized tag set,
// a hash over that canonical set, and the compiled case-insensitive matcher.
type Classifier struct {
	Tags    []string
	Hash    string
	Matcher *regexp.Regexp
}

// QualityKind discriminates the quality specification.
type QualityKind string

const (
	QualityBitrate QualityKind = "bitrate"
)

// Quality is the tagged quality spec for an encoded output. v1 supports
// bitrate only (kbps).
type Quality struct {
	Kind    QualityKind `json:"kind"`
	Bitrate int         `json:"bitrate,omitempty"`
}

// AudioOutputSpec is one desired managed audio output.
type AudioOutputSpec struct {
	Codec   Codec    `json:"codec"`
	Quality *Quality `json:"quality,omitempty"`
}

// Lossless reports whether the output spec is a lossless class.
func (s AudioOutputSpec) Lossless() bool { return IsLosslessCodec(s.Codec) }

// DesiredProfile is the exact managed audio set for one partition.
type DesiredProfile struct {
	Lossless *AudioOutputSpec `json:"lossless,omitempty"`
	Encoded  *AudioOutputSpec `json:"encoded,omitempty"`
}

// Conversion modes (ADR 0005 §3). The zero value and the empty string are
// the strict behavior: old policies and old clients keep byte-identical
// outcomes. "available_sources" is the relaxed mode for new batches.
const (
	ModeStrict           = "strict"
	ModeAvailableSources = "available_sources"
)

// Policy maps each classifier partition to a complete DesiredProfile. The
// user declares desired outputs and a set of literal content tags only;
// conversion/cleanup mechanics are derived by the planner. Mode selects the
// decision rule and applies to both partitions.
type Policy struct {
	SchemaVersion  int            `json:"schema_version"`
	Mode           string         `json:"mode,omitempty"`
	ClassifierTags []string       `json:"classifier_tags,omitempty"`
	Matched        DesiredProfile `json:"matched"`
	Unmatched      DesiredProfile `json:"unmatched"`
}

// ProfileFor resolves the desired profile of one partition. It is the single
// source of the partition -> profile mapping: the planning skeleton, both
// decision tables and the execution freeze side all call it.
func ProfileFor(policy Policy, part Partition) DesiredProfile {
	if part == PartitionUnmatched {
		return policy.Unmatched
	}
	return policy.Matched
}

// Component states.
const (
	StatusOK      = "ok"
	StatusBlocked = "blocked"
)

// Stable reason/error codes (machines, not prose).
const (
	ReasonSourceMissing       = "SOURCE_MISSING"
	ReasonSourceAmbiguous     = "SOURCE_AMBIGUOUS"
	ReasonTargetPathAmbiguous = "TARGET_PATH_AMBIGUOUS"
	ReasonTargetPathConflict  = "TARGET_PATH_CONFLICT"
	// ReasonConformanceUnconfirmed is an observed file of the declared codec
	// that no generation credential proves was written for this target. It
	// replaced QUALITY_UNKNOWN, which said the same thing about a bitrate no
	// probe could establish.
	ReasonConformanceUnconfirmed = "CONFORMANCE_UNCONFIRMED"
	ReasonLosslessUnfulfillable  = "LOSSLESS_TARGET_UNFULFILLABLE"
)

// Summary reasons.
const (
	ReasonActionable = "ACTIONABLE"
	ReasonNoMatch    = "NO_MATCH"
	ReasonBlocked    = "BLOCKED"
	ReasonPartial    = "PARTIAL"
)

// Lane names.
const (
	LaneLossless = "lossless"
	LaneEncoded  = "encoded"
)

// Lane-wide decisions.
const (
	LaneKeepAll    = "KEEP_ALL"
	LaneRebuildAll = "REBUILD_ALL"
	LaneRebuild    = "REBUILD"
	LaneKeep       = "KEEP"
	LaneBlocked    = "BLOCKED"
)

// Per-file resolutions.
const (
	ResolutionKeep   = "keep"
	ResolutionDelete = "delete"
	ResolutionEncode = "encode"
)

// Operation kinds.
const (
	OpKindEncode         = "encode"
	OpKindRemoveObsolete = "delete_obsolete"
)

// Fixed phases for the audio step. The planner emits materialize and remove
// operations; validate/commit belong to future Execute and are named here as
// the explicit phase contract.
const (
	PhaseMaterializeOutputs  = "materialize_outputs"
	PhaseValidateOutputs     = "validate_outputs"
	PhaseCommitOutputs       = "commit_outputs"
	PhaseRemoveObsoleteAudio = "remove_obsolete_audio"
)

// LaneDecision summarizes one output lane's component-wide outcome.
type LaneDecision struct {
	Lane       string `json:"lane"`
	Decision   string `json:"decision"`
	ReasonCode string `json:"reason_code,omitempty"`
	Message    string `json:"message,omitempty"`
}

// FileDecision is the reviewable conclusion for one file. KEEP decisions are
// never executable operations; they exist so the review shows the final set.
type FileDecision struct {
	Path       string `json:"path"`
	Resolution string `json:"resolution"`
	ReasonCode string `json:"reason_code,omitempty"`
	Message    string `json:"message,omitempty"`
	TargetPath string `json:"target_path,omitempty"`
}

// VariantDecision groups the per-file decisions of one stem.
type VariantDecision struct {
	Stem      string         `json:"stem"`
	Decisions []FileDecision `json:"decisions"`
}

// Operation is one planned executable change.
type Operation struct {
	Kind        string   `json:"kind"`
	Phase       string   `json:"phase"`
	ComponentID string   `json:"component_id"`
	VariantStem string   `json:"variant_stem"`
	SourcePath  string   `json:"source_path"`
	TargetPath  string   `json:"target_path,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

// ComponentOutcome is the full reviewable outcome for one Component.
type ComponentOutcome struct {
	ComponentID        string            `json:"component_id"`
	Partition          Partition         `json:"partition"`
	Status             string            `json:"status"`
	ReasonCode         string            `json:"reason_code,omitempty"`
	Message            string            `json:"message,omitempty"`
	Lanes              []LaneDecision    `json:"lanes"`
	Variants           []VariantDecision `json:"variant_decisions"`
	Operations         []Operation       `json:"operations"`
	ProjectedInventory []string          `json:"projected_inventory"`
	Files              []FileTuple       `json:"files"`
}

// StepSummary aggregates the audio step outcomes. UnmetTargets counts stems
// kept with a target that could not be satisfied (relaxed mode), whether the
// shape was out of reach or an existing file of it could not be confirmed; it
// is independent of OperationCount and BlockedCount.
type StepSummary struct {
	ComponentCount int    `json:"component_count"`
	BlockedCount   int    `json:"blocked_count"`
	OperationCount int    `json:"operation_count"`
	ErrorCount     int    `json:"error_count"`
	UnmetTargets   int    `json:"unmet_targets,omitempty"`
	SummaryReason  string `json:"summary_reason"`
}

// ReconcileInput is the fully-resolved input to the audio step planner.
type ReconcileInput struct {
	RootPath   string
	Entries    []AudioEntry
	Policy     Policy
	Classifier Classifier
}

// ReconcileResult is the planned audio step.
type ReconcileResult struct {
	Digest     string // inventory fingerprint over recognized audio
	Count      int    // number of recognized audio entries
	Components []ComponentOutcome
	Summary    StepSummary
}
