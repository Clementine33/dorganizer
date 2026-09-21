package maintenance

import (
	"context"
	"log"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// pass runs one maintenance pass and reports whether it ran to completion.
//
// Completion is about the work, not about success: a database that is already
// clean, and one that cannot give pages back at all, are both finished with.
// An incomplete pass - refused by admission, out of budget, or stopped by an
// error - is retried on the next tick instead of waiting out the interval, so a
// application that is busy every time still gets its cleanup.
func (l *Loop) pass(ctx context.Context) bool {
	s := &slot{acquire: l.acquire}
	defer s.drop()

	started := time.Now()
	freeBefore, pages, pageSize := l.size(ctx)

	deleted, complete := l.deleteBatches(
		ctx, s,
		started.Add(-l.opts.ScanRetention),
		started.Add(-l.opts.GenerationRetention),
	)

	reclaimed := 0
	if complete {
		reclaimed, complete = l.reclaimPages(ctx, s)
	}

	walBusy, walFrames, walCopied := 0, 0, 0
	if complete {
		walBusy, walFrames, walCopied, complete = l.checkpointWAL(ctx, s)
	}

	freeAfter, _, _ := l.size(ctx)
	log.Printf(
		"maintenance: complete=%t deleted_scan_and_generation_rows=%d reclaimed_pages=%d "+
			"freelist=%d->%d pages=%d page_size=%d wal(busy=%d frames=%d copied=%d) elapsed_ms=%d",
		complete, deleted, reclaimed, freeBefore, freeAfter, pages, pageSize,
		walBusy, walFrames, walCopied, time.Since(started).Milliseconds(),
	)
	return complete
}

// deleteBatches deletes retention batches until one of them finds nothing left,
// the budget runs out, or a task takes the slot. It reports the rows deleted
// and whether the tables are clean.
func (l *Loop) deleteBatches(
	ctx context.Context,
	s *slot,
	scanCutoff, generationCutoff time.Time,
) (int64, bool) {
	if !s.hold() {
		return 0, false
	}
	var deleted int64
	for batch := 0; ; batch++ {
		stats, err := l.repo.RunRetentionCleanupBatch(ctx, scanCutoff, generationCutoff, l.opts.BatchRows)
		if err != nil {
			log.Printf("maintenance: retention batch failed: %v", err)
			return deleted, false
		}
		rows := stats.DeletedScanSessions + stats.DeletedGenerations
		deleted += rows
		if rows == 0 {
			return deleted, true
		}
		if batch+1 >= l.opts.MaxBatches {
			// Budget spent with rows still due: the rest waits for the next tick,
			// which keeps any single visit to the database short.
			return deleted, false
		}
		if !s.yield(ctx, l.opts.YieldDelay) {
			return deleted, false
		}
	}
}

// reclaimPages returns free pages to the filesystem a step at a time, and
// reports how many went and whether there is nothing left to return.
//
// A database that was not created with incremental auto-vacuum is skipped
// outright, and says so: no step could move a page there, and an operator whose
// file never shrinks is owed the reason. Otherwise a step that returns pages
// without shrinking the file is progress, not failure - in WAL mode the
// truncation waits for a checkpoint that can copy the whole log.
func (l *Loop) reclaimPages(ctx context.Context, s *slot) (int, bool) {
	if !s.hold() {
		return 0, false
	}

	mode, err := l.repo.AutoVacuumMode(ctx)
	if err != nil {
		log.Printf("maintenance: auto_vacuum probe failed: %v", err)
		return 0, false
	}
	if mode != sqlite.AutoVacuumIncremental {
		log.Printf(
			"maintenance: not reclaiming: the database was created with auto_vacuum=%d, and only an "+
				"incremental one can return free pages; point ONSEI_DATA_DIR at a new directory to get that",
			mode,
		)
		return 0, true
	}

	free, err := l.repo.FreePageCount(ctx)
	if err != nil {
		log.Printf("maintenance: read freelist failed: %v", err)
		return 0, false
	}
	reclaimed := 0
	for step := range l.opts.MaxVacuumSteps {
		if free == 0 {
			return reclaimed, true
		}
		if step > 0 && !s.yield(ctx, l.opts.YieldDelay) {
			return reclaimed, false
		}
		moved, err := l.repo.ReclaimFreePages(ctx, l.opts.VacuumPages)
		if err != nil {
			log.Printf("maintenance: reclaim failed: %v", err)
			return reclaimed, false
		}
		reclaimed += moved
		if moved == 0 {
			// The freelist did not shrink: a reader is holding the log back, or
			// those pages are not free to give. Either way this loop has nothing
			// more to do, and a later pass is where it gets retried.
			return reclaimed, true
		}
		if free, err = l.repo.FreePageCount(ctx); err != nil {
			log.Printf("maintenance: read freelist failed: %v", err)
			return reclaimed, false
		}
	}
	// Budget spent with pages still free: the rest waits for the next tick.
	return reclaimed, false
}

// checkpointWAL copies the write-ahead log into the database file, which is
// what turns reclaimed pages into a smaller file, and reports the pragma's
// three values. It never retries with a mode that would wait for readers: an
// incomplete copy is a normal outcome that the next pass improves on.
func (l *Loop) checkpointWAL(ctx context.Context, s *slot) (busy, frames, copied int, ok bool) {
	if !s.hold() {
		return 0, 0, 0, false
	}
	busy, frames, copied, err := l.repo.CheckpointWAL(ctx)
	if err != nil {
		log.Printf("maintenance: checkpoint failed: %v", err)
		return 0, 0, 0, false
	}
	return busy, frames, copied, true
}

// size reads the numbers the report line carries. They never decide whether the
// pass runs, so a failure is logged and reported as zero.
func (l *Loop) size(ctx context.Context) (free, pages, pageSize int) {
	var err error
	if free, err = l.repo.FreePageCount(ctx); err != nil {
		log.Printf("maintenance: read freelist failed: %v", err)
	}
	if pages, err = l.repo.PageCount(ctx); err != nil {
		log.Printf("maintenance: read page count failed: %v", err)
	}
	if pageSize, err = l.repo.PageSize(ctx); err != nil {
		log.Printf("maintenance: read page size failed: %v", err)
	}
	return free, pages, pageSize
}
