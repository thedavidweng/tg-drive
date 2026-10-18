# 0052: Persist directory move destinations and progress

## Status

Accepted

## Context

Directory moves commit files separately. A failure after one rename leaves
both source and destination directories in the index. Repeating the original
command can then interpret the newly created destination as a containing
directory and incorrectly append the source basename. An error without
durable counters also hides which portion finished.

## Decision

Schema version 6 records each directory move before its first remote write.
Store the requested destination separately from the resolved destination.
For the same channel, source and requested destination, the latest unfinished
operation supplies the resolved destination on retry. Preflight conflict
validation and the existing path locks still apply.

Checkpoint each attempted source and completed rename. Failed operations
return their UUID and moved/failed/remaining counts in the existing error
details. Retrying creates a new history record, preserving the earlier
failure; completed operations do not redirect subsequent moves.

## Consequences

Existing single-file and successful move output stays unchanged. A database
reopen preserves partial-move routing and history. This is an explicit retry
of the remaining indexed files, not automatic replay or a transaction spanning
Telegram and SQLite. Crash repair and cancellation remain separate semantics.
