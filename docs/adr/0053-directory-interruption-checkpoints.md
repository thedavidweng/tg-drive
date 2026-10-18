# 0053: Finalize directory progress after cancellation

## Status

Accepted

## Context

Directory moves and deletes use the operation context for remote calls and
SQLite progress updates. When Telegram returns cancellation, the same context
cannot persist the failed checkpoint: a SQL cancellation replaces the original
error and leaves the history record running.

## Decision

Finalize each started directory operation once, before releasing its locks.
Use a context detached from caller cancellation and bounded to five seconds
for this local terminal update only. All remote writes and running checkpoints
retain the original cancellable context. A final checkpoint failure remains an
error.

Keep partial counters and the last attempted path on cancellation. Stop before
starting the next file when cancellation is observed. Preserve Telegram flood
wait retry details and require an explicit retry of remaining indexed files.

## Consequences

Cancellation during a Telegram call returns the existing ERR_CANCELLED error
with durable operation history. No schema or remote metadata changes are
needed. A hard process termination or remote write that outlives its local
commit still requires repair; finalization is not automatic crash replay.
