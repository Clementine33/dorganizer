# 10. Self-cleanup of the conversion database

Date: 2026-09-22

## Status

Accepted. Revises "auto-compaction is not added in this iteration" in ADR 0007
§84 and replaces the startup-only retention pass that decision left in place.
The idempotency-key horizon of ADR 0004 §4 is unchanged: the generation window
below is the same 30 days, and it still has to stay aligned with it.

## Context

`cache.db` is the only SQLite file the application opens
(`cmd/onsei-organizer-backend/main.go`). Two properties of it had drifted apart
from what the retention policy assumed:

- Retention ran once per process start. A long-lived process deleted nothing
  after that, so the file grew with the sessions the application created.
- Deleting rows frees pages into the freelist, where later writes reuse them,
  but the file itself never shrinks. The freelist is therefore invisible: the
  file size is a high-water mark that no policy ever lowered.

Auditing the deletion conditions turned up two defects that had to be fixed
before any of this ran periodically rather than once:

- The scan delete judged rows by `COALESCE(finished_at, started_at)` with no
  status filter, so a scan running longer than the 7-day window was eligible for
  deletion from under its own process. Running that once at startup made it
  unlikely to fire; running it daily would not.
- A process killed mid-scan leaves a `queued`/`running`/`merging` row behind.
  `HasActiveScanForRoot` treats those states as a live scan, so the row refused
  planning and execution against its root indefinitely, and the age-based delete
  was the only thing that ever cleared it.

## Decision

### 1. Retention deletes terminal rows only, and startup finalizes what a dead process left

A scan session is deletable when its status is terminal
(`completed`, `failed`, `canceled`, `interrupted`) and its terminal timestamp is
past the cutoff. A queued/running/merging row belongs to a scan that is in
flight, or to one whose process died, and the database is not the authority on
which — so it is never deleted on age alone.

What it is instead is finalized: at startup, before the dispatcher starts,
`InterruptStaleScanSessions` marks non-terminal scan rows `interrupted` and
gives them a `finished_at`. This mirrors `InterruptStaleGenerations` and
`InterruptStaleExecutions`, and it does double duty: the row stops refusing its
root, and it starts its retention clock from the moment it was finalized — never
from its long-past `started_at`, so a crash is not deleted the instant it is
noticed.

Planning sessions keep `finished_at IS NOT NULL` as their terminal test, which
is how they were already judged.

### 2. Maintenance is an in-process pass that runs only while the application is idle

One loop per process, started at process start and stopped by the same context
that stops the HTTP server. It never runs outside the process lifetime: no
scheduler, no daemon, no endpoint.

Idleness is not a property the loop can ask about, so it does what the direct
file-management path does — it acquires the process-wide admission slot
(`Gate.BeginMaintenance`, ADR 0007 §5) and lets the answer be a refusal. Holding
that slot is what makes "idle" hold: a scan, a file operation or a session
enqueue that arrives while it is held is refused rather than started alongside,
and a task that arrives first refuses the pass. Covering scans, planning and
execution in one check is the reason the pass reuses this slot instead of
testing each of them itself.

The slot is held for one small batch at a time and released between batches,
because a refusal is an answer the user has to act on. Each pass also has a
budget (batches, then pages); when it runs out the pass stops and resumes on the
next tick, which is also what happens when it is refused. Only a pass that
finished its work is deferred for a whole interval. The loop makes no promise
about how long it holds the slot — batches are bounded by rows and pages, and
the pass's own log line carries the elapsed time.

### 3. Only a database created with incremental auto-vacuum can give pages back

`PRAGMA auto_vacuum` is fixed when the file is created: set before the first
table exists it is written into the header, and afterwards it is a silent no-op
that only a full `VACUUM` could change. The connection pragmas therefore ask for
incremental auto-vacuum, which every new database picks up and no existing one
changes, and the mode is read back to decide whether reclaiming is worth
attempting at all.

Consequently an existing database keeps working exactly as before — cleanup
runs, reclamation is skipped — and the log says so once, naming the one action
that would change it (`ONSEI_DATA_DIR` at a new directory). Deliberately not
done: converting the file with a one-time full `VACUUM` (it needs working space
equal to the database and blocks everything while it runs), and bumping the
schema generation to force a reset (ADR 0007 §7 already keeps that lever for
schema changes, not for housekeeping).

### 4. Reclaiming is deferred, not fought for

`PRAGMA incremental_vacuum` moves free pages off the end of the file, and in WAL
mode the resulting truncation happens at a checkpoint that manages to copy the
whole log — not at the commit. The pass therefore ends with a PASSIVE
checkpoint, which never waits for a reader: an incomplete checkpoint is a normal
result that a later pass improves on. It is not retried with `TRUNCATE`, which
would wait for the same readers while holding the admission slot, and would
block the very tasks the slot exists to protect.

### 5. The SQLite underneath had to be fixed first

The embedded SQLite is 3.51.2, which is inside the range of the WAL-reset
corruption bug fixed in 3.51.3 (`sqlite.org/changes.html`, 2026-03-13: "Fix the
WAL-reset database corruption bug"; the bug needs concurrent writers and a
checkpoint — the shape this application already had, and one this decision adds
another checkpoint to). The driver is upgraded to a release embedding SQLite
3.51.3 or later before this work lands, as its own commit.

That upgrade is not neutral, and the regression run is why we know: 3.51.3 also
made SQLite refuse a deferred transaction's snapshot upgrade that it previously
applied silently — losing the concurrent commit in the process, which is what
the corruption fix was about. Every write path here reads before it writes
(find the queued session, load the frozen draft, then claim or persist), so a
deferred transaction could lose that race and get `SQLITE_BUSY_SNAPSHOT`
instead of doing its work. On the upgraded driver, a repeated run of the workset
package failed intermittently — a claimed generation or a persisted revision
that never arrived.

Two changes, in that commit:

- Read-write transactions begin immediate (`_txlock=immediate` on the DSN), so a
  transaction owns the write lock before it reads and cannot lose a snapshot it
  is about to write on. Read-only transactions stay deferred — the driver only
  applies the mode when the transaction is not read-only — so the auto-vacuum
  probe and the report readers are unaffected.
- A claim that fails is retried after a pause and reported, instead of being
  treated like an empty queue. Parking on the wake channel was the second half
  of the bug: the session that failed to claim is already queued, so no further
  enqueue was ever going to wake the worker, and the queue stalled silently
  until an unrelated session arrived.

## Consequences

- What this does not promise: a bound on the database file. It bounds how long
  the two session ledgers are kept and returns the pages they free. Inventory,
  execution results and staging rows are not cleaned here, and data inside the
  windows still grows — the file size is not a function of the retention
  windows.
- Freelist pages are reused by later writes, so a delete is not wasted space
  even before a checkpoint: what is recovered is the file's high-water mark, not
  the bytes of the deleted rows.
- Reclamation can lag the cleanup by several passes when readers hold the log
  back. That is the accepted trade for never blocking a reader.
- The pass is observable only through its log line, which carries rows deleted,
  pages reclaimed, the freelist and page counts before and after, and the
  checkpoint's three values. This is deliberate: the failure mode of this
  feature is silence (a database that was created without incremental
  auto-vacuum, a statement that moves one page per call, a checkpoint that never
  runs), and the log is what tells the four apart. The regression test that
  fails on all four is the one that reclaims pages in a real temp database and
  asserts the file got smaller.
- Rejected: a queryable idle flag on the gate (the gate's refusal is the
  answer, and a separate check would race the start it was meant to exclude); a
  maintenance priority queue (the pass competes for the same slot, and a
  deferred pass loses nothing by waiting a tick); cleaning execution results and
  staging in the same change (their retention rules are a separate decision, and
  their windows are not aligned with the idempotency horizon).