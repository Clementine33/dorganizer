package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/onsei/organizer/backend/internal/inventory"
)

// ObservedAudioEntries reads the observed audio files at or beneath one root,
// each with the generation credential recorded for it when there is one. The
// subtree is matched as a binary range rather than with LIKE, for the two
// reasons subtreeFilePredicateSQL gives: SQLite's LIKE is ASCII
// case-insensitive, which would conflate case-distinct POSIX siblings and read
// a name holding % or _ as a pattern, and it cannot use the path index.
// Everything under a directory sorts between prefix+"/" and prefix+"0" ("0" is
// the next character after "/"), where the prefix is the root without its
// trailing slash — so the root "/" is matched by the file's own row or by
// anything under it. The caller passes the stored POSIX form.
func (r *Repository) ObservedAudioEntries(rootPath string) ([]inventory.ObservedAudio, error) {
	prefix := strings.TrimSuffix(rootPath, "/")
	rows, err := r.db.Query(`
		SELECT e.path, COALESCE(e.size, 0), COALESCE(e.mtime, 0), COALESCE(e.bitrate, 0), COALESCE(e.format, ''),
		       g.codec, g.bitrate_kbps, g.mode, g.size, g.mtime
		FROM entries e LEFT JOIN generation_records g ON g.path = e.path
		WHERE e.is_dir = 0 AND (e.path = ? OR (e.path >= ? AND e.path < ?))
	`, rootPath, prefix+"/", prefix+"0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]inventory.ObservedAudio, 0)
	for rows.Next() {
		var e inventory.ObservedAudio
		var codec, mode sql.NullString
		var bitrateKbps, recordSize, recordMtime sql.NullInt64
		if err := rows.Scan(
			&e.Path, &e.Size, &e.Mtime, &e.Bitrate, &e.Format,
			&codec, &bitrateKbps, &mode, &recordSize, &recordMtime,
		); err != nil {
			return nil, err
		}
		if codec.Valid {
			e.Generated = &inventory.GeneratedCredential{
				Codec:       codec.String,
				BitrateKbps: int(bitrateKbps.Int64),
				Mode:        mode.String,
				Size:        recordSize.Int64,
				Mtime:       recordMtime.Int64,
			}
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// RootExistsInInventory reports whether the planning root itself is present in
// the scanned entries (directory or file row). A folder that was never scanned,
// or whose scan removed it, is absent.
func (r *Repository) RootExistsInInventory(rootPath string) (bool, error) {
	var n int
	err := r.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", rootPath).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check root existence: %w", err)
	}
	return n > 0, nil
}
