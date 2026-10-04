# 0044: td rm reports a stale record in its error details

Status: Accepted.

Context: `td rm` removes the media first, then redacts or deletes the
file's machine record (a per-file `td-manifest:v1` record, or the shared
`td-album:v1` inventory of an album member). When the record write fails,
the file is already gone from Telegram and its row is marked deleted, but
the command fails with `ERR_TELEGRAM_RPC` unless `--allow-stale-manifest`
is set. The success result carries `stale_manifest: true`; the error
envelope carried nothing beyond the message text, so a script reading the
failure could not tell that the delete itself had happened, and the service
result that held the outcome was dropped on the error path.

Decision: The stale-record failure keeps its code (`ERR_TELEGRAM_RPC`) and
exit code (4) and adds machine-readable `details`: `path`, `mode`,
`stale_manifest: true`, and `manifest_message_id`. `DeleteFile` returns its
result together with that error. The message says the file was removed
and no longer suggests a rerun, which would fail with
`ERR_REMOTE_NOT_FOUND` because the row is already deleted. A tombstone whose comment edit fails, but
whose tombstone caption lands, is not stale: the caption outranks the live
comment during scans (ADR 0014), so the delete stays sticky.

Consequences:

- Callers can distinguish "removed, record stale" from a failed delete
  without parsing the message. `--allow-stale-manifest` decides the outcome
  up front; it cannot be applied after the fact.
- The change is additive: existing consumers of the code and exit code are
  unaffected.
