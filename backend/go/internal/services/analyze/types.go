package analyze

// OpType discriminates a single-action plan operation.
type OpType string

const (
	OpTypeConvert OpType = "convert"
	OpTypeDelete  OpType = "delete"
)

// Operation is a single planned single-action operation.
type Operation struct {
	Type       OpType
	SourcePath string
	TargetPath string
	Reason     string
}

// Entry is a stored audio entry used by the bitrate enrichment bridge. The
// workflow reconcile package has its own AudioEntry; this type remains for
// single-action planning and analyzer integration.
type Entry struct {
	PathPosix string
	FileSize  int64
	Bitrate   int64
	Format    string
}
