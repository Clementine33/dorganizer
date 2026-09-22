package inventory

import "time"

// Entry is a row from the entries table used for building folder trees.
type Entry struct {
	Path       string
	ParentPath string
	Name       string
	IsDir      bool
	Size       int64
	Mtime      int64
	Bitrate    *int32
	Format     string
}

// InventoryFile is one observed file fact to write into the entries inventory
// after an execution changed it on disk.
type InventoryFile struct {
	Path  string // persisted POSIX form
	Size  int64
	Mtime int64
}

// GenerationRecord is one generation credential: the target this app generated
// a file for, the encoder that wrote it, and the facts that bind the record to
// those bytes. A row is written only for an output that passed the executor's
// stream and full-decode verification and committed — the row's presence is the
// verification, which is why there is no column repeating it. The size and mtime
// are what a plan compares against the inventory (the same content proxy the
// scanner and the fingerprint use); the hash is what keeps a deeper re-check
// possible later.
type GenerationRecord struct {
	Path           string
	Codec          string
	Encoder        string
	EncoderVersion string
	BitrateKbps    int
	Mode           string // cbr | vbr
	Size           int64
	Mtime          int64
	ContentSHA256  string
	CreatedAt      time.Time
}
