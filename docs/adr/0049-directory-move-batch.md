# 0049: Move indexed directory contents as one operation

## Status

Accepted

## Context

`td mv` already renames one indexed file by rewriting its Telegram machine
record and SQLite row under path locks. Directory move was rejected even
though the index already has enough information to enumerate its files.

## Decision

`td mv` enumerates active files below a selected directory, resolves each
source-relative destination, validates every destination against paths outside
the source subtree, and acquires all source and destination path locks before
the first Telegram mutation. It then applies the existing per-file rename
protocol in stable path order and runs directory GC.

The operation is deliberately not presented as a transaction across Telegram:
a remote failure can leave a partial move. The original source remains a
valid retry selector for files still indexed below it. Empty directories have
no Telegram representation and remain subject to existing ephemeral-node
semantics. Directory delete and forum/multi-channel routing remain separate
work.

## Consequences

Directory move now supports nested files and existing destination directories
without a new schema or Telegram protocol. Validation and locking prevent
known destination conflicts and concurrent path mutations; a failure after a
successful file rename is observable and retryable rather than silently
reported as complete.
