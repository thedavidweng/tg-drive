# 0028: File record module

Status: Accepted

Context: A file's machine record is either its own `td-manifest:v1`
comment (or a legacy reply or `td:v1` caption) or its entry in the
`td-album:v1` inventory that its media group shares. The row's
`manifest_message_id` points at whichever one holds the file, so the index
cannot tell the two apart. `td rm`, `td mv`, `td repair <path>`, and
`cp --replace` each worked out membership separately: they fetched the
manifest message, branched on album versus per-file, and wrote to
Telegram themselves. As a result, rm had two copies of the "mark deleted,
collect directories, report stale manifest" tail, and every new caller
risked redacting a shared inventory and dropping its siblings' records.

Decision: `internal/service` has one File record module (`fileRecord`,
`file_record.go`). Callers state an intent, and the module resolves
membership once per operation through the row's manifest carrier:

- `Retire(mode)` handles `td rm`. It deletes or tombstones the file, falls
  back to a caption tombstone when a comment edit fails, and reports a
  stale record (or an `ERR_DB` sibling-record failure) for the caller to
  surface after it marks the row deleted.
- `RetireSuperseded(mode)` handles `cp --replace`. It does the same work on
  a best-effort basis and keeps the retirement sticky.
- `Rename(dst)` handles `td mv`.
- `Rewrite()` handles `td repair <path>`.

Album members are removed from the shared inventory and their media is
deleted in every delete mode. Membership is still resolved from Telegram,
and no schema column is added. The caller keeps operation locks and the
file row status.

Consequences: Album handling for the commands that change one file now
lives in one place, and callers no longer branch on record kind. Scans,
adopt, album upload, and hash repair still use the inventory primitives
(`loadAlbumManifest`, `writeAlbumManifest`) directly. CLI behaviour, JSON
output, and the storage contract are unchanged.
