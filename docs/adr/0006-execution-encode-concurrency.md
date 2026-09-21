# 6. Bounded encode concurrency in execution sessions

Date: 2026-09-17

## Status

Accepted.

## Context

Execution sessions are globally serialized (one at a time) and, inside a
session, walk frozen units one after another: each unit materializes every
output, validates them, commits, then removes obsolete audio before the next
unit starts. Every encode is a single-threaded `ffmpeg` process, so a session
leaves three of four cores idle while a folder's files are converted in turn,
and one large file blocks every later folder.

Measurements on the real workload (2.67 GiB of WAV, 6 files in one component,
target mp3 320 kbps), taken with `cmd/io-bench` over the share and
`scripts/io-bench-nas.py` on the storage machine:

| Strategy | Desktop (i5-12400F, over CIFS) | NAS (i3-9300T, local disk) |
|---|---|---|
| serial | 100.8 s (1.10 cores) | 149.1 s (1.14 cores) |
| 2 encode workers | 53.5 s (1.88x) | 83.1 s (1.79x) |
| 4 encode workers | 46.0 s (2.19x) | 59.6 s (2.50x) |
| SSD staging | 129.0 s (0.78x) | not run |

The disk never exceeded 9% busy in any run, and per-file encodes inflate
15–25% at four workers. The remaining ceiling is the batch's largest single
file, whose encode is single-threaded and cannot be split.

## Decision

1. **Sessions stay globally serialized.** One execution session runs at a
   time, so two worksets never write overlapping roots and a scan never
   overlaps an execution. This record changes only what happens inside a
   session.

2. **Inside a session, one shared pool of N encode workers pulls file tasks
   across components and member folders.** The work unit is one frozen encode
   task — `encode → probe → full-decode validation`, exactly what
   `FFmpeg.Encode` already performs. The pool is filled in frozen order, so
   earlier components are encoded first. Delivery is cancel-aware on a bounded
   queue, a component is registered as a managed unit before any of its tasks
   is delivered, and workers drain after cancellation — a queued task that
   never starts work still completes its accounting, and no send can block the
   stop path. Delivery ends exactly once — the coordinator closes the queue
   after the last unit or when teardown begins — and the workers drain it
   before exiting, so waiting for them always terminates.

3. **A bounded component preparation window `W = N` spans the whole session.**
   At most N components are prepared and uncommitted at once; a component that
   has finished encoding but not yet committed still holds its slot. The
   window bounds how stale a precheck may be by the time its files encode, how
   many components' staged files may coexist, and how much work a failure
   discards. It does not bound a component's output count, the queue, or a
   component's wait for its turn: a slow head keeps its slot, later components
   cannot enter while the window is full, and workers may idle. That idle is
   accepted as the price of a simple window. Member folders are never a
   batching boundary — the window and the pool cross them freely.

4. **One coordinator commits components strictly in frozen order.** When the
   head component's encodes have all returned, the coordinator re-probes every
   staged output (the existing validate check — no repeated full decode),
   commits with a same-directory rename, removes obsolete audio, and persists
   progress and the report. Commit, removal, recovery copies, inventory sync
   and persistence therefore stay single-threaded and race-free. Every stop
   signal — a sibling's failure as much as a user cancellation — is honoured
   **between commit operations**, exactly as today: an already-started rename
   or removal finishes, so a recovery copy is never destroyed halfway. No
   stronger guarantee is claimed: an I/O failure can still leave a component
   partially committed, and such a component reports its partial facts
   (committed/removed/remaining/recovery, its inventory synced) instead of
   being downgraded to pending. Post-stop writes therefore never widen beyond
   today's single operation. Progress is written as soon as a head is known —
   at the start of each commit-order iteration, before preparation or delivery
   can block, including N = 1 when the window empties after each commit. (Each
   boundary now publishes the next head along with the result it commits, so
   only the first iteration needs a write of its own; the observable rule is
   unchanged. See ADR 0009.) Empty
   executions retain their existing completion/cancellation rules and start
   no worker pool. A sealed notification is not a permanent commit permit:
   the coordinator rechecks the stop before admission, and `Commit` checks it
   before mutations and between operations.

5. **Failure and cancellation are fail-fast and do not roll back.** One
   teardown sequence serves every stop — encode failure, prepare or commit
   failure, user cancellation. All use one concurrency-safe stop entry point
   that records the first real error and its unit index before canceling the
   shared session context (context cancellation and canceled components are
   never real errors). Encode tasks and `Commit` observe that same context:
   a worker's failure stops a sibling's commit at the next operation boundary
   without waiting for the coordinator to receive a notification. Admission
   stops, the coordinator closes the task queue exactly once, and waits for
   every worker and its encoder child to exit. Only then does it finalize the
   open components and persist the terminal state. A prepare failure is attributed to the unit being
   prepared — its index is kept even without a prepared unit — and never to
   the head. Only the component whose task failed is marked failed; a
   component stopped mid-commit keeps its partial facts; a component whose
   commit never began stays pending, with its staged temps removed, the
   operations it never ran kept on its entry — the report still names that
   range — and any recovery path the cleanup could not remove recorded there
   too. The
   session is canceled only when no real operation error was recorded by the
   time all in-flight work returned; otherwise the first real error decides
   the failure. Cancellation-induced errors never replace it. Committed
   components are never rolled back, and an innocent uncommitted component
   that a serial run would have committed stays pending instead — that is the
   accepted price of fail-fast, not an unchanged behaviour.

6. **No cross-disk staging.** SSD staging measured 0.78x: it serializes reads
   that the encoder otherwise overlaps with CPU work, and the disk is not the
   constraint in either environment. Outputs are staged as `*.tmp.<token>`
   beside their targets and committed with a same-filesystem rename, exactly
   as today; the executor performs no cross-disk moves.

7. **The pool size is a constructor parameter** (`0` = auto =
   `min(4, runtime.NumCPU())`), with `W = N`. A user setting may override it
   later; it is not exposed in the draft or the API.

## Consequences

- Measured wall time improves 1.79–2.50x at N = 2–4 on the reference
  workloads. The ideal floor is `max(largest file, total CPU / N)`; window idle
  and 15–25% per-file inflation at N = 4 keep real runs above it. The floor
  bounds the model — it is not a promise the scheduler can make, and a slow
  head can still idle workers behind the window.
- Concurrency is bounded by the number of open components, not by the session
  size: precheck staleness, discard scope and the count of components holding
  staged files all scale with N. A component's own staged-file count and a
  component's wait for its commit turn are not bounded by the window.
- Per-file latency inflates 15–25% at N = 4 while throughput still improves;
  sessions stay cooperative at every commit operation and every encode task,
  with the stop boundaries unchanged from the serial executor.
- No API, report, storage or frontend contract shapes change. The one
  deliberate behaviour change is the fail-fast outcome in Decision 5 — an
  innocent uncommitted component a serial run would have committed stays
  pending — and it is recorded here rather than hidden behind "unchanged
  behaviour". `Task` gains `PrepareUnit`/`PreparedUnit` in place of `RunUnit`
  (ADR 0005's seam, same opacity guarantees); `execute.RunComponent` remains as
  a serial wrapper.
- The staging alternative stays rejected: revisiting it requires new evidence
  that the storage path, not the CPU, limits a session.
