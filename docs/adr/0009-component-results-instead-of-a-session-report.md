# 9. One result row per component instead of a session report

Date: 2026-09-22

## Status

Accepted. Replaces the `report_json` decision of ADR 0004 §4 as it is restated
in ADR 0006 decision 4; the encode-concurrency rules themselves are unchanged.

## Context

A session persisted its whole per-component report on its own row and rewrote
that row at every component boundary — twice, counting the write that publishes
the next head. With N components every write serializes an N-entry report
(pending entries included), so a session's cumulative serialization and commit
volume grew with N²: two full rewrites per component, each one carrying what
every component before it had already done.

The measurements below are one session of 1000 components, 2 operations each,
on the same machine (i5-12400F, local disk), replaying each write shape through
the repository API with the WAL held open (`wal_autocheckpoint = 0`), and
timing the whole session including the JSON serialization each write did:

| | report rewritten per boundary | one result row per component |
|---|---|---|
| session wall time | 4.98 s | 0.19 s |
| WAL bytes per component | 419,684 | 13,321 |
| total WAL for the session | ≈ 420 MB | ≈ 13 MB |

The rewrite also made reads expensive in a way that had nothing to do with what
a caller asked for: the cancel watchdog (every 500 ms) and the event stream
(every 1 s) both read the whole row, report included, to look at a cancel flag
and a counter.

## Decision

1. **A component's result is its own row**, `execution_component_results`, keyed
   `(execution_id, component_index)`, written in one short transaction that also
   advances the session's counters and its position. Encoding and file operations
   stay outside that transaction, exactly as before; a component's row is
   committed as it finishes, so the facts of everything that already happened are
   on disk and nothing rewrites them.
2. **`plan_executions.report_json` is gone.** The row keeps the session state:
   status, counters, current position, cancel flag, errors. The frozen component
   list is not duplicated — it is the session's frozen worklist, which the
   assembly joins the results onto.
3. **The counters are derived, not incremented.** The boundary transaction
   recomputes `completed_components`/`completed_operations` from the stored
   results (`status <> 'pending'`, sum of completed operations), which makes a
   replayed write idempotent by construction: an uncertain commit can be retried
   without double-counting, and a component that never ran cannot be counted.
4. **A component with no result row is pending.** ADR 0004's rule that a session
   names what it never executed survives because the worklist still does: an
   interrupted session lists every component, with empty results.
5. **Reads split by purpose.** The cancel watchdog and the event stream poll a
   control-only projection (status, cancel flag, counters, position). The detail
   read assembles the frozen worklist with the recorded results, optionally
   paged by component index; the SSE snapshot is that same assembled payload, and
   the stream then sends each component's own entry as its result lands, before
   any terminal event that follows it.
6. **A result write that keeps failing stops the session.** It is retried a
   bounded number of times (only the write, never the encode or the commit that
   produced it) and then reported as a session failure: a component that
   committed on disk but whose result is not durable is not described as if it
   were, and no later write is assumed to repair it.
7. **The schema generation moves to 3.** Removing the column changes what a
   stored row means, so an older database is refused at open time and the
   operator points `ONSEI_DATA_DIR` at a new directory (ADR 0007 §7, spec D2).
   No migration is written; nothing about the old report is carried over.

## Consequences

- Cumulative result serialization and transfer go from O(N²) to O(N + total
  result size). Reading a full report is still O(N), and index maintenance on
  the result rows is proportional to the rows written — the win is in what is
  *written and rewritten*, not in what a full read costs.
- The wire payload does not change shape: `components[]` entries are still
  assembled from the frozen worklist plus the observed facts, so clients keep
  reading the same fields.
- The event stream becomes incremental: a client can fill a run in as it goes
  without re-reading the detail, which is what the frontend does now. A client
  that misses events (attached late, paged, reconnected) still converges through
  the detail read and the terminal event's calibration.
- `docs/api.md` no longer promises that a detail read is enough to see a
  component's facts at any moment; it promises that the component's own entry is
  pushed, and that the detail read is the fallback.
- Teardown facts — the unrun operations and preserved leftovers of components
  that were open when a session stopped — are written as those components'
  result rows during teardown, since the terminal write no longer carries a
  report. A failure while writing them is logged: the disk state they describe
  is unchanged by it, and the session ends either way.
