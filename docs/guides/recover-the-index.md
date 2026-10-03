# Recover the index

How to rebuild the local SQLite index from Telegram, and how to repair
inconsistencies without touching Telegram.

Output blocks copied verbatim from real runs are unmarked (personal values
are masked). Blocks that depend on your own data are prefixed *Illustrative*.

---

## Rebuild after database loss

**Scenario:** you deleted `local_cache.db` (or moved to a new machine) and
`td ls` no longer knows your files. Telegram messages are the source of
truth; the database is only a cache.

A fresh database does not know which channel is your drive, so bind it
again. `td init` runs a full scan of the channel it binds:

```sh
td init ~/Pictures --bind-channel            # pick from a list
td init ~/Pictures --bind-channel="Pictures [TD]"
```

*Illustrative:*

```text
initialized ~/Pictures -> channel "Pictures [TD]" (id <channel-id>)
indexed 1240 existing files from Telegram; next: td tree /
```

If the database still knows the channel (for example the index only looks
stale), rebuild it in place:

```sh
td scan --full
```

*Illustrative:*

```text
scan complete (full): 1240 active, 0 deleted, 3 invalid, 0 missing
```

The counts mean:

- **active** — files reconstructed with valid metadata
- **deleted** — tombstoned files (shown with `--include-deleted`)
- **invalid** — messages that could not be parsed; inspect them instead of
  trusting a partial index (`--strict` turns them into a failure)
- **missing** — indexed paths whose Telegram message disappeared

A full scan resumes if interrupted: rerun the same command and it continues
from the last checkpoint.

Verify the result:

```sh
td status
```

Example (identifiers masked):

```text
account        logged in as <name> (user <user-id>)
channel        Pictures [TD] (id <channel-id>)
local root     ~/Pictures
files          12 active
last scan      2026-08-14T22:17:54Z (last full: 2026-08-14T22:17:54Z)
upload limit   2.0 GB per file
database       ~/.local/share/tg-drive/local_cache.db
health         ok
```

**Next step:** browse with `td tree /` or download with `td get`.

## Refresh after drift

**Scenario:** messages were edited or deleted directly in a Telegram client,
and an incremental scan does not pick up old-message changes.

```sh
td scan
```

Captured from a real run (empty channel):

```text
scan complete (incremental): 0 active, 0 deleted, 0 invalid, 0 missing
```

Incremental scans are cheap but blind to old edits; when in doubt, go full:

```sh
td scan --full
```

## Repair pending uploads

**Scenario:** an upload was interrupted and `td status` reports
`stale_pending`. The retry either completes the upload or clears the stale
state.

```sh
td repair --pending
```

Captured from a real run (nothing pending):

```text
invalid: 0
locks_cleared: 0
orphaned: 0
repaired: 0
skipped: 0
```

## Repair orphaned uploads

**Scenario:** a crash happened after Telegram accepted the media but before
the index was written. The message exists on Telegram but no file row points
at it, so retrying `td cp` would duplicate it.

Run the repair — it completes each orphaned upload (regenerates the
metadata, resends the manifest reply, promotes the row) and needs no
`--confirm` because it deletes nothing:

```sh
td repair --orphaned
```

Captured from a real run (nothing orphaned):

```text
deleted: 0
invalid: 0
repaired: 0
```

If you would rather discard the half-uploaded message instead of completing
it:

```sh
td repair --orphaned --delete-orphaned --confirm
```

*Illustrative:*

```text
deleted: 1
invalid: 0
repaired: 0
```

`--confirm` is required because this deletes Telegram messages.

## Repair scan errors

**Scenario:** a scan recorded per-message errors (`scan_errors_pending` > 0)
and you want to retry just those.

```sh
td repair --scan-errors
```

*Illustrative:*

```text
pending: 2
resolved: 2
```

## Next steps

- [Troubleshoot](troubleshoot.md) — diagnose what is wrong before repairing
- [Adopt an existing channel](adopt-an-existing-channel.md) — claim
  messages that were never managed by `td`
- [How td works](how-td-works.md) — why Telegram stays the source of truth
