# 0024: File row lifecycle in sqlitestore

Status: Accepted

Context: File row status transitions were raw SQL inline across
`internal/service`, and the same transition was written differently at
different call sites. `deleted` cleared `node_id` inside a transaction in
`td rm` but was a bare statement elsewhere; scan's supersede of an older
duplicate ran outside the batch transaction that indexed its replacement, so
a failed batch left the path with no active row; album inventory and
manifest record row updates ignored their errors after Telegram was already
mutated.

Decision: `adapters/native/sqlitestore` owns the File row lifecycle. Each
transition is one intent-named method with a single definition:
`StagePending`, `RecordMessage`, `DiscardUpload`, `MarkOrphaned`,
`MarkInvalid`, `MarkDeleted`, `MarkDeletedByMessage`, `MarkMissing` (inside
the full-scan finalizer's transaction), supersede through
`FileIndexRequest.ReplaceFileID`, and `SetManifest` / `DetachManifest` for
machine-record pointers; `ActiveByPath` and `PendingByPath` are the path
lookups. Every row leaving `active` or `pending` clears `node_id` and drops
its resumable upload state in the same transaction. Services call these
methods and propagate their errors as `ERR_DB` instead of writing `files`
SQL.

Consequences: Scan supersede commits or rolls back with the replacement's
index batch. A machine-record row update that fails after the Telegram write
now fails the command with `ERR_DB` (run `td scan --full` to reconcile);
`td rm` still marks the row deleted first, and a failed album upload deletes
the inventory it sent. Bulk listings and the counts behind `td status` stay
as queries in the services.
