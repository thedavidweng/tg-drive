# 0030: Operation module

Status: Accepted. Changes how the path-level `operation_locks` of
DECISIONS.md are taken; the locks themselves are unchanged.

Context: AGENTS.md requires an operation lock around every remote write,
but nothing enforced it; checking meant reading every use case. Each use
case resolved the bound channel on its own, often more than once (about 22
row-id lookups and 19 Telegram-id lookups, each a query, plus five
discussion-group lookups). Thirteen call sites took locks, and they built
the keys in different ways. Some writes ran with no lock at all.
`td adopt --rewrite-captions` deleted manifest replies and edited captions
unlocked. `td import saved` sent the `td-origin:v1` record and deleted the
Saved Messages source after the upload had already released the
destination lock.

Decision: `internal/service` has one Operation module (`operation.go`).

- `channel(ctx)` resolves the channel context (row id, Telegram id) once
  per use case. The discussion group, which carries machine records, is
  resolved on first use and then kept. Resolving it lazily keeps reads,
  scans, and deletes working on legacy channels that have no linked group.
  Use cases that write records still call it before their first write, so
  they fail early, as before.
- `operate(ctx, ch, paths, fn)` runs `fn` as an operation. It builds the
  path lock keys, acquires them with the existing TTL, heartbeat, sorted
  order, and `ERR_OPERATION_LOCKED` semantics, and passes the operation to
  `fn` through the context. A nested operation reuses the channel context
  and the locks its parent already holds, and acquires only the missing
  keys. This is how the upload pipeline runs inside an import. An
  operation with no paths covers writes to messages that no indexed path
  owns.
- Every use case that writes to Telegram goes through `operate`.
  Caption rewrites and reply deletes in adopt hold the paths of the
  affected indexed files. An import holds each destination for the whole
  sequence of upload, origin record, and source delete. Lock acquisition
  is non-blocking, so this nesting cannot deadlock.
- In the service tests, the Telegram client is wrapped so that every
  remote write must happen inside an operation whose locks are live.

Consequences: A use case cannot write to Telegram outside an operation
without failing the service tests. Adopt caption rewrites and import
annotations now fail with `ERR_OPERATION_LOCKED` when another process
holds the path. They report this per item and honor `--continue-on-error`.
The lock table, key format, TTL, and error codes are unchanged. Discussion
group linking is a channel-level write and takes no path locks.
