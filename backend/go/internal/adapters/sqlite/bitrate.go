package sqlite

import (
	"math/rand"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/inventory"
)

// bitrateUpdateBatchSize is how many rows one batched statement covers.
const bitrateUpdateBatchSize = 100

// bitratePersistRetryLimit is the maximum number of retries after the first
// attempt for SQLITE_BUSY/SQLITE_LOCKED errors during bitrate persistence.
const bitratePersistRetryLimit = 3

// bitratePersistRetryBase is the base delay for retry backoff.
const bitratePersistRetryBase = 50 * time.Millisecond

// UpdateEntryBitrates writes probed bitrates back onto the entries table.
//
// batch selects between the two write shapes the planner's configuration
// offers, and they differ in what a failure leaves behind:
//
//   - false: one statement per row, no transaction, stopping at the first
//     failure — the rows before it are already written;
//   - true: rows chunked by bitrateUpdateBatchSize into one transaction, so a
//     failure in a later chunk rolls the earlier ones back.
//
// Both retry a lock the same way: the first attempt, then up to
// bitratePersistRetryLimit more with a growing delay and jitter, and only for
// a busy or locked database — any other error is returned as it came.
//
// The mutex is held across the whole retry sequence, not per attempt: it
// serializes the concurrent planner goroutines sharing this repository, which
// is what keeps them from making each other busy in the first place.
func (r *Repository) UpdateEntryBitrates(updates []inventory.BitrateUpdate, batch bool) error {
	if len(updates) == 0 {
		return nil
	}

	r.BitrateWriteMu.Lock()
	defer r.BitrateWriteMu.Unlock()

	var lastErr error
	for attempt := 0; attempt <= bitratePersistRetryLimit; attempt++ {
		if attempt > 0 {
			// Linear backoff with small jitter.
			delay := bitratePersistRetryBase * time.Duration(attempt)
			jitter := time.Duration(rand.Int63n(int64(bitratePersistRetryBase)))
			time.Sleep(delay + jitter)
		}

		err := r.updateEntryBitratesOnce(updates, batch)
		if err == nil {
			return nil
		}
		if !isSQLiteBusyLockedError(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// updateEntryBitratesOnce performs one write attempt in the shape batch picks.
func (r *Repository) updateEntryBitratesOnce(updates []inventory.BitrateUpdate, batch bool) error {
	if !batch {
		for _, update := range updates {
			if _, err := r.db.
				Exec(
					"UPDATE entries SET bitrate = ?, updated_at = datetime('now') WHERE path = ?",
					update.BitrateKbps,
					update.Path,
				); err != nil {
				return err
			}
		}
		return nil
	}

	chunks := chunkBitrateUpdates(updates, bitrateUpdateBatchSize)
	if len(chunks) == 0 {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, chunk := range chunks {
		query, args := buildBatchBitrateUpdateQuery(chunk)
		if _, err := tx.Exec(query, args...); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true

	return nil
}

// isSQLiteBusyLockedError reports whether an error is the database refusing the
// write because someone else holds it.
func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked")
}

// chunkBitrateUpdates splits the updates into chunks of at most chunkSize rows.
func chunkBitrateUpdates(updates []inventory.BitrateUpdate, chunkSize int) [][]inventory.BitrateUpdate {
	if chunkSize <= 0 || len(updates) == 0 {
		return nil
	}

	chunks := make([][]inventory.BitrateUpdate, 0, (len(updates)+chunkSize-1)/chunkSize)
	for start := 0; start < len(updates); start += chunkSize {
		end := min(start+chunkSize, len(updates))
		chunks = append(chunks, updates[start:end])
	}

	return chunks
}

// buildBatchBitrateUpdateQuery renders one chunk as a single CASE/WHEN update.
// The ELSE keeps an unlisted path's bitrate as it is; the WHERE IN limits the
// statement to the rows the chunk names.
func buildBatchBitrateUpdateQuery(chunk []inventory.BitrateUpdate) (string, []any) {
	var b strings.Builder
	b.Grow(128 + len(chunk)*32)

	b.WriteString("UPDATE entries SET bitrate = CASE path")
	args := make([]any, 0, len(chunk)*3)
	for _, u := range chunk {
		b.WriteString(" WHEN ? THEN ?")
		args = append(args, u.Path, u.BitrateKbps)
	}
	b.WriteString(" ELSE bitrate END, updated_at = datetime('now') WHERE path IN (")
	for i, u := range chunk {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
		args = append(args, u.Path)
	}
	b.WriteByte(')')

	return b.String(), args
}
