package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

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

// upsertGenerationRecord writes one credential inside the caller's transaction.
// The whole row is replaced: a record describes exactly one generation, and a
// path that was regenerated has no field left from the generation before it.
func upsertGenerationRecord(tx *sql.Tx, g GenerationRecord) error {
	_, err := tx.Exec(`
		INSERT OR REPLACE INTO generation_records
			(path, codec, encoder, encoder_version, bitrate_kbps, mode, size, mtime, content_sha256, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, g.Path, g.Codec, g.Encoder, g.EncoderVersion, g.BitrateKbps, g.Mode, g.Size, g.Mtime,
		g.ContentSHA256, g.CreatedAt.Format(timeFormat))
	if err != nil {
		return fmt.Errorf("record generation of %s: %w", g.Path, err)
	}
	return nil
}
