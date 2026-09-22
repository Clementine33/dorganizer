package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/onsei/organizer/backend/internal/inventory"
)

// upsertGenerationRecord writes one credential inside the caller's transaction.
// The whole row is replaced: a record describes exactly one generation, and a
// path that was regenerated has no field left from the generation before it.
func upsertGenerationRecord(tx *sql.Tx, g inventory.GenerationRecord) error {
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
