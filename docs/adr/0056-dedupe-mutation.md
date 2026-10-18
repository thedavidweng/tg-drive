# 0056: Make duplicate cleanup explicit and hash-checked

## Status

Accepted

## Context

`td dedupe` already reports duplicate indexed content, but the report alone
cannot reclaim remote storage. Deleting a duplicate is destructive and the
audit can become stale while another operation changes the tree.

## Decision

Keep `td dedupe` read-only by default. Add `--delete --confirm` to retain the
lexicographically first path in every duplicate hash group and retire the
remaining indexed files through the existing delete protocol. Acquire locks
for every path in each group, then re-read the keeper and each target and
require the stored content hash and size to match the audit. Stream each
member's remote bytes through BLAKE3 before the first Telegram mutation so
a stale index cannot authorize cleanup with a missing or changed keeper.
This intentionally downloads every selected copy, without buffering the
whole body or writing local files.

The command reports the planned group count, retained count, deleted count,
deleted paths and the audit's reclaimable-byte estimate. A failed operation
returns the existing structured error with the successful deletion progress;
already retired rows are not selected by a subsequent explicit retry.
Deletion follows the configured policy: tombstoning does not free media
bytes. The byte estimate is never a claim of actual reclaimed storage.

## Consequences

No new storage schema or dependency is needed. Cleanup is not a transaction
across Telegram and SQLite: a later remote failure can leave earlier duplicate
members retired, but the index records each successful retirement and the
command exposes completed protocol progress. A remote mutation followed by
an index failure can be missing from that progress; explicit full scan
reconciliation is required before retrying. No batch history or automatic
crash replay is introduced. Files without hashes remain untouched. Locks
exclude cooperating td writes, not changes made outside td after verification.
