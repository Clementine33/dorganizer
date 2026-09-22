package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// ==================== Database Maintenance ====================
//
// The primitives here serve the idle-time maintenance pass: it deletes the rows
// retention no longer keeps (see repo_retention.go) and then hands the pages
// those deletes freed back to the filesystem. Each one is a pragma rather than
// a statement, and their effect depends on how the database file was created,
// so they live apart from the retention deletes they follow.

// Auto-vacuum modes as PRAGMA auto_vacuum reports them.
const (
	AutoVacuumNone        = 0
	AutoVacuumFull        = 1
	AutoVacuumIncremental = 2
)

// AutoVacuumMode reports the auto-vacuum mode the database file was created
// with, which is the mode that decides whether free pages can be returned to
// the filesystem at all (AutoVacuumIncremental is the only one that can).
//
// Both statements run on one reserved connection, and a schema row is read
// before the pragma. Until a connection has locked the database it answers
// PRAGMA auto_vacuum from the value its DSN set in memory - every connection
// carries connectionPragmas, and on a file that has tables that setting never
// reaches the header - so asking any pooled connection would report incremental
// vacuum on a database that has none. Reading a schema row loads the file, and
// pinning the pragma to that same connection keeps the answer the file's.
func (r *Repository) AutoVacuumMode(ctx context.Context) (int, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("reserve connection for auto_vacuum probe: %w", err)
	}
	defer func() { _ = conn.Close() }()

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, fmt.Errorf("begin auto_vacuum probe tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var tables int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema").Scan(&tables); err != nil {
		return 0, fmt.Errorf("load database for auto_vacuum probe: %w", err)
	}

	var mode int
	if err := tx.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return 0, fmt.Errorf("read auto_vacuum mode: %w", err)
	}
	return mode, nil
}

// FreePageCount reports how many pages of the file hold no data. They are
// allocated, they are reused by later writes, and on an incremental auto-vacuum
// database ReclaimFreePages can return them to the filesystem.
func (r *Repository) FreePageCount(ctx context.Context) (int, error) {
	return r.pragmaInt(ctx, "freelist_count")
}

// PageCount reports the total number of pages in the database file.
func (r *Repository) PageCount(ctx context.Context) (int, error) {
	return r.pragmaInt(ctx, "page_count")
}

// PageSize reports the page size in bytes.
func (r *Repository) PageSize(ctx context.Context) (int, error) {
	return r.pragmaInt(ctx, "page_size")
}

// ReclaimFreePages asks SQLite to remove up to `pages` pages from the freelist
// and shrink the file by that much, and reports how many pages actually left
// the freelist. A database that is not in incremental auto-vacuum mode reclaims
// none: the btree layer reads the mode from the file header and stops before
// touching a page, which is why an older database needs no special case here.
//
// The pragma is a loop that can yield a row per page, and the driver steps a
// statement once before resetting it, so the rows have to be drained for more
// than one page to move. `pages` is never handed a non-positive value: SQLite
// reads a missing or non-positive argument as "reclaim the entire freelist".
//
// The count comes from the freelist rather than from the rows the pragma
// emitted: that is the number the caller acts on, and it does not depend on how
// many rows this build of SQLite chooses to emit. Pages a concurrent writer
// takes from the freelist in the meantime can only make it read lower.
func (r *Repository) ReclaimFreePages(ctx context.Context, pages int) (int, error) {
	if pages < 1 {
		pages = 1
	}
	freeBefore, err := r.FreePageCount(ctx)
	if err != nil {
		return 0, err
	}

	rows, err := r.db.QueryContext(ctx, "PRAGMA incremental_vacuum("+strconv.Itoa(pages)+")")
	if err != nil {
		return 0, fmt.Errorf("start incremental_vacuum(%d): %w", pages, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
	}
	if err = rows.Err(); err != nil {
		return 0, fmt.Errorf("drain incremental_vacuum(%d): %w", pages, err)
	}

	freeAfter, err := r.FreePageCount(ctx)
	if err != nil {
		return 0, err
	}
	if freeAfter >= freeBefore {
		return 0, nil
	}
	return freeBefore - freeAfter, nil
}

// CheckpointWAL backfills the write-ahead log into the database file as far as
// the current readers allow, and reports the pragma's three values: whether a
// lock was missed, the size of the log in frames, and how many frames were
// copied.
//
// This is what turns a smaller page count into a smaller file: in WAL mode the
// truncation a reclaim asks for is deferred to a checkpoint that manages to
// copy the whole log. PASSIVE never waits for a reader, so an incomplete
// checkpoint (busy, or checkpointed < log) is normal - it means the backfill
// happens later, and it is never retried here with a mode that would block.
func (r *Repository) CheckpointWAL(ctx context.Context) (busy, log, checkpointed int, err error) {
	if err := r.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &checkpointed); err != nil {
		return 0, 0, 0, fmt.Errorf("wal checkpoint: %w", err)
	}
	return busy, log, checkpointed, nil
}

// pragmaInt reads a pragma that answers with a single integer. The name is
// always a literal from this file, never caller input: pragma arguments cannot
// be bound as parameters.
func (r *Repository) pragmaInt(ctx context.Context, name string) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&n); err != nil {
		return 0, fmt.Errorf("read pragma %s: %w", name, err)
	}
	return n, nil
}
