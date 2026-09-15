package workset

import (
	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// conversionTask is the conversion Task: reconciliation of the workset's audio
// inventory against the draft's desired outputs. Its draft document is the
// sparse DraftDoc and its kind is OperationTypeConversion.
//
// The implementation still lives inside the workset package — the seam has a
// single adapter for now; the conversion module moves behind it as a whole.
type conversionTask struct {
	configDir string
}

func newConversionTask(configDir string) conversionTask {
	return conversionTask{configDir: configDir}
}

func (conversionTask) Kind() string { return OperationTypeConversion }

// SeedDraft builds the initial sparse draft of a new conversion operation: no
// member records at all (everyone participates and inherits the common
// settings), mode available_sources stored explicitly, tag literals copied
// from config.json's prune.literal_tags, and wav + mp3@320 outputs so a new
// workset is immediately usable.
func (t conversionTask) SeedDraft() ([]byte, string, int) {
	doc := &DraftDoc{
		SchemaVersion:  DraftSchemaVersion,
		Mode:           reconcile.ModeAvailableSources,
		ClassifierTags: appconfig.LoadPruneLiteralTags(t.configDir),
		Matched:        defaultProfile(),
		Unmatched:      defaultProfile(),
	}
	if doc.ClassifierTags == nil {
		doc.ClassifierTags = []string{}
	}
	raw, hash, err := MarshalDraft(doc)
	if err != nil {
		raw, hash = "{}", ""
	}
	return []byte(raw), hash, DraftSchemaVersion
}

func (conversionTask) ValidateDraft(raw []byte, members []*sqlite.WorksetMember) error {
	doc, err := ParseDraft(string(raw))
	if err != nil {
		return NewError(ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is not valid", err)
	}
	return validateDraftDoc(doc, members)
}

func (conversionTask) NormalizeDraft(raw []byte, members []*sqlite.WorksetMember) ([]byte, string, int, error) {
	doc, err := ParseDraft(string(raw))
	if err != nil {
		return nil, "", 0, NewError(ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is not valid", err)
	}
	normalized := normalizeDraft(doc, members)
	out, hash, err := MarshalDraft(normalized)
	if err != nil {
		return nil, "", 0, NewError(ErrKindInternal, "INTERNAL", "failed to encode draft", err)
	}
	return []byte(out), hash, DraftSchemaVersion, nil
}

func defaultProfile() reconcile.DesiredProfile {
	return reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded: &reconcile.AudioOutputSpec{
			Codec:   reconcile.CodecMp3,
			Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
		},
	}
}
