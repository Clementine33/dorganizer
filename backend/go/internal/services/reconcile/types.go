package reconcile

import "regexp"

// Core domain types for the reconcile_audio_outputs workflow step.
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
	}
	return ""
}

// IsLosslessCodec reports whether the codec is a lossless source class.
func IsLosslessCodec(c Codec) bool {
	return c == CodecWav || c == CodecFlac
}

// AudioEntry is an observed recognized-audio file. Size and Mtime feed the
// metadata inventory fingerprint; Bitrate is an enriched fact (0 = unknown).
type AudioEntry struct {
	PathPosix string
	Size      int64
	Mtime     int64
	Bitrate   int64
	Format    string
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

// Policy maps each classifier partition to a complete DesiredProfile. The
// user declares desired outputs and a set of literal content tags only;
// conversion/cleanup mechanics are derived by the planner.
type Policy struct {
	SchemaVersion  int            `json:"schema_version"`
	ClassifierTags []string       `json:"classifier_tags,omitempty"`
	Matched        DesiredProfile `json:"matched"`
	Unmatched      DesiredProfile `json:"unmatched"`
}

// Component states.
const (
	StatusOK      = "ok"
	StatusBlocked = "blocked"
)

// Stable reason/error codes (machines, not prose).
const (
	ReasonSourceMissing         = "SOURCE_MISSING"
	ReasonSourceAmbiguous       = "SOURCE_AMBIGUOUS"
	ReasonTargetPathAmbiguous   = "TARGET_PATH_AMBIGUOUS"
	ReasonTargetPathConflict    = "TARGET_PATH_CONFLICT"
	ReasonQualityUnknown        = "QUALITY_UNKNOWN"
	ReasonLosslessUnfulfillable = "LOSSLESS_TARGET_UNFULFILLABLE"
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

// StepSummary aggregates the audio step outcomes.
type StepSummary struct {
	ComponentCount int    `json:"component_count"`
	BlockedCount   int    `json:"blocked_count"`
	OperationCount int    `json:"operation_count"`
	ErrorCount     int    `json:"error_count"`
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
