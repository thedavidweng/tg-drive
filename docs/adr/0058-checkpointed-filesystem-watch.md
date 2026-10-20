# 0058: Checkpointed one-way filesystem watch

## Status

Accepted

## Context

Transfer watching observes existing jobs; it does not synchronize local file
content. A directory watcher also needs to distinguish its completed uploads
from unrelated remote files and from changed remote content after restart.

## Decision

Scan regular file contents periodically using an opened local root. Reject
symlinks and special files. Use the existing BLAKE3 identity, upload pipeline,
retirement protocol, operation locks and binding routing. Require explicit
confirmation and hashing; do not infer two-way synchronization from mtimes.

Persist completed file hashes and sizes in schema 8, keyed by channel row,
absolute local directory, canonical remote directory and relative file path.
Pin the account/peer/topic/root binding while the watch runs; refuse a rebound
root. Lock all planned destinations and verify actual remote bytes before
replacement or deletion. Adopt a matching committed upload if its checkpoint
was not persisted. Changed remote content or missing owned content conflicts
instead of being silently replaced or resurrected.

Upload stable private snapshots and verify their planned hashes. Apply new and
changed files before optional removals, so a failed rename destination retains
the old copy. Without --delete, retain remote files missing locally. With it,
retire only checkpointed paths whose remote bytes still match the baseline.
Save each successful file checkpoint; stop on error or cancellation. Restart is
explicit and retains completed work. Empty directories are not synchronized.

## Consequences

A cancelled watch can resume without resending unchanged verified content.
Polling reads local contents and verified remote contents each pass; it trades
bandwidth for explicit content conflict detection. Checkpoints are local
ownership records, not recoverable Telegram manifests: after losing the index,
full scan recovers file content, but cannot authorize deleting removed local
files. No automatic conflict choice, inferred rename, bidirectional sync,
watch-time dedupe, or silent hashing substitute is introduced.
