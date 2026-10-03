# JSON contract

All JSON command output uses an envelope.

## Success

```json
{
  "ok": true,
  "data": {},
  "meta": {
    "command": "cp",
    "duration_ms": 125,
    "schema_version": "2026-07-29",
    "request_id": "...",
    "warnings": []
  }
}
```

## Error

```json
{
  "ok": false,
  "error": {
    "code": "ERR_CODE",
    "message": "human readable message",
    "category": "api",
    "retryable": true,
    "retry_after_ms": 5000,
    "details": {}
  },
  "meta": {
    "command": "cp",
    "duration_ms": 10,
    "schema_version": "2026-07-29",
    "request_id": "..."
  }
}
```

`ERR_TELEGRAM_RATE_LIMITED` errors carry machine-readable retry hints in
`details`:

```json
{
  "ok": false,
  "error": {
    "code": "ERR_TELEGRAM_RATE_LIMITED",
    "message": "telegram rate limited this account: retry after 23h41m26s (at 2026-07-26 13:15 PDT)",
    "details": {
      "retry_after_seconds": 85286,
      "retry_at": "2026-07-26T20:15:00Z"
    }
  }
}
```

`ERR_SESSION_LOCKED` (category `internal`, retryable, exit code 5) means
another process holds the Session lock on the same Telegram session file and
did not release it within `locks.session_wait_seconds`. `details.wait_seconds`
is the bound that passed. Commands that read only the local index never take
the Session lock, so they never return this code.

```json
{
  "ok": false,
  "error": {
    "code": "ERR_SESSION_LOCKED",
    "message": "the Telegram session is in use by another td process; waited 30s, retry when it finishes",
    "category": "internal",
    "retryable": true,
    "details": {
      "wait_seconds": 30
    }
  }
}
```

`ERR_CANCELLED` (category `cancelled`, `retryable: true`) reports a command
stopped by SIGINT or SIGTERM. Retrying is safe: a large upload resumes from
its confirmed parts.

`ERR_TRANSFER_NOT_FOUND` (category `validation`, exit code 2) means no
Transfer has the ID `td transfers show` was given.

`ERR_DIRECTORY_MOVE_UNSUPPORTED` and `ERR_DIRECTORY_DELETE_UNSUPPORTED` carry
the offending remote directory in `details.path`.

## Version

```json
{
  "ok": true,
  "data": {
    "version": "0.1.0",
    "commit": "abc123",
    "date": "2026-07-02T00:00:00Z",
    "built_by": "goreleaser"
  }
}
```

## Upload result (single file)

```json
{
  "ok": true,
  "data": {
    "path": "/Pictures/2024/beach.jpg",
    "channel_id": "123456789",
    "message_id": 8821,
    "manifest_message_id": null,
    "size": 2482911,
    "hash": "blake3:fullhexvalue",
    "invite_link": "https://t.me/+Abc123"
  }
}
```

The `invite_link` field is omitted when the channel has no public/join link.
`manifest_message_id` is the machine record's message id; since ADR 0018 it
lives in the linked discussion group's comment thread (the row's
`manifest_chat_tg_id` records the peer), so the id is not addressable in the
drive channel itself.

A retry that adopted a pending upload and sent only its unconfirmed parts adds
`"resumed": true` (files above 10 MB; the identity — size and content hash —
must match the interrupted attempt).

## Status

`td status` succeeds before `td init`:

```json
{
  "ok": true,
  "data": {
    "initialized": false,
    "authenticated": true,
    "user_id": 42,
    "display_name": "Test User",
    "channels": [],
    "db_path": "/home/you/.local/share/tg-drive/local_cache.db"
  }
}
```

Once a channel is bound it adds the selected channel and index counters:

```json
{
  "ok": true,
  "data": {
    "initialized": true,
    "channel_id": "1001",
    "channel": {"channel_id": "1001", "title": "Pictures [TD]", "local_root": "/home/you/Pictures"},
    "channels": [{"channel_id": "1001", "title": "Pictures [TD]", "local_root": "/home/you/Pictures"}],
    "files": {"active": 12, "deleted": 1},
    "last_scan_at": "2026-08-14T22:17:54Z",
    "last_full_scan_at": "2026-08-14T22:17:54Z",
    "last_scanned_message_id": 16,
    "scan_errors_pending": 0,
    "orphaned": 0,
    "stale_pending": 0,
    "stale_locks": 0,
    "upload_states": 0,
    "upload_limit_bytes": 2147483648,
    "db_path": "/home/you/.local/share/tg-drive/local_cache.db"
  }
}
```

`authenticated`, `user_id`, and `display_name` are omitted when Telegram is
unreachable. JSON paths are absolute; human output abbreviates the home
directory to `~`.

## Init

`td init` reports the bound channel and the result of its initial full scan:
`indexed_files` (active files found in the channel) on success, or
`scan_error` (message) when the scan failed and `td scan --full` should be
rerun. A re-run on an already bound root returns `already_initialized: true`
instead.

## Doctor

Every check is `pass`, `warn`, `fail`, or `unknown` (not checked, usually
because an earlier check failed). `hints` carries a fix for non-passing
checks. Checks: `config`, `session_file`, `file_permissions`, `auth`, `db`,
`db_wal`, `channel`, `history_read`, `upload`, `delete`, `invite_link`,
`edit_old_caption`, `discussion`, `file_size_limit`, `caption_counter`,
`path_codec`, `saved_history`, `saved_delete`.

`td doctor` includes the Saved Messages capabilities in both the human checks
and JSON data:

```json
{
  "ok": true,
  "data": {
    "checks": {
      "saved_history": "pass",
      "saved_delete": "pass"
    },
    "saved_history_ok": true,
    "saved_delete_ok": true
  }
}
```

`saved_history_ok` gates reading `td import saved`; `saved_delete_ok` reports
whether verified source cleanup is available. The values may be `false` or
absent from a partial/unknown capability probe.

## Upload result (multi-file album)

`td cp <local...> <remote-dir>` and `td cp --recursive` report aggregate
counters plus one entry per sent media group. A lone survivor after skips is
an ordinary single upload: it counts toward `uploaded` but appears in no
group.

```json
{
  "ok": true,
  "data": {
    "uploaded": 12,
    "skipped": 0,
    "errors": [],
    "albums": [
      {
        "grouped_id": 730001,
        "reply_message_id": 8835,
        "message_ids": [8821, 8822, 8823],
        "paths": ["/albums/one.bin", "/albums/two.bin", "/albums/three.bin"]
      }
    ],
    "channel_id": "-100123456789",
    "invite_link": "https://t.me/+Abc123"
  }
}
```

- `uploaded` / `skipped` / `errors` mirror the recursive counters; with the
  default fail policy a planning conflict aborts the whole command before any
  Telegram write, so `errors` stays empty on success.
- `albums` lists every media group in send order; each carries Telegram's
  `grouped_id`, the `td-album:v1` inventory message's id (a comment in the
  linked discussion group per ADR 0018), member message ids, and member
  paths. Empty (`[]`) when nothing grouped.
- `--recursive` emits the same shape plus its historical keys.

## NDJSON event stream

Long-running commands such as `td cp --events` emit one JSON envelope per line:

```json
{"ok":true,"data":{"file_name":"big.bin","part":5,"part_size":524288,"uploaded":2621440,"total":4294967296},"meta":{"command":"cp.progress","duration_ms":120,"schema_version":"2026-07-29","request_id":"..."}}
{"ok":true,"data":{"path":"/big.bin","message_id":1234,"size":4294967296},"meta":{"command":"cp","duration_ms":4200,"schema_version":"2026-07-29","request_id":"..."}}
```

A single-file `td cp --events` also emits `transfer.stage` events, one each
time its Transfer enters a stage: `queued`, then `hashing` (when the file is
hashed), `uploading`, `publishing`, and finally `completed`, `failed`, or
`cancelled`.
They are interleaved with the `cp.progress` lines in the order the stages
happen, before the final `cp` line (or error envelope). `data` is the
Transfer as `td transfers show` returns it, at the moment it entered the
stage:

```json
{"ok":true,"data":{"id":"6f1c...","kind":"upload","stage":"uploading","channel":"1001","source":"/home/me/big.bin","dest":"/big.bin","bytes_done":0,"bytes_total":12582912,"items_done":0,"items_total":1,"front_end":"cli","cancel_requested":false,"created_at":"2026-10-02T10:40:01.927632222Z","updated_at":"2026-10-02T10:40:01.931002117Z"},"meta":{"command":"transfer.stage","duration_ms":4,"schema_version":"2026-07-29","request_id":"..."}}
```

## Transfers

`td transfers show <id>` returns one Transfer (ADR 0033); `td transfers
list` returns `{"transfers": [...]}`, newest first, `[]` when none match.
`td transfers retry <id>` re-runs a `failed`, `cancelled`, or `interrupted`
Transfer and returns it once it ends, in the same shape; the Transfer keeps
its `id` and `created_at`, and the retrying process becomes its owner. A
retry of a `running` or `completed` Transfer fails with `ERR_USAGE`, an
unknown ID with `ERR_TRANSFER_NOT_FOUND`, and a failed re-run reports the
call's own error envelope, exactly as the equivalent `cp`/`get` would.
`--events` emits one `transfer.stage` event per stage the retried Transfer
enters and, for upload kinds, the `cp.progress` lines `td cp --events`
emits — a resumed upload reports only the parts it actually sends — then a
final `transfers.retry` line carrying the ended Transfer.

`td transfers watch --events` (or `--json`) streams `transfer.stage`
events — the same payload shape as `td cp --events` emits — for the
Transfers of every process, not just the watching one. The watch rereads
the index every 250 ms and reports what it observes: a Transfer already
running enters the stream at its current stage, and one that passes a
stage between two polls is reported at its later stage, so events may skip
stages. A Transfer that already ended when the watch started is history
and emits nothing. The stream runs until Ctrl-C, which closes it with the
usual `ERR_CANCELLED` error envelope (exit 130); without `--json` that
error is human text on stderr and the stdout stream holds `transfer.stage`
events only.

```json
{
  "ok": true,
  "data": {
    "id": "6f1c2a7e-3b0d-4c55-9a43-2f0a1b7c9d10",
    "kind": "upload",
    "stage": "cancelled",
    "channel": "1001",
    "source": "/home/me/big.bin",
    "dest": "/big.bin",
    "bytes_done": 2097152,
    "bytes_total": 12582912,
    "items_done": 0,
    "items_total": 1,
    "front_end": "cli",
    "cancel_requested": false,
    "created_at": "2026-10-02T10:40:01.927632222Z",
    "updated_at": "2026-10-02T10:40:02.610447301Z",
    "finished_at": "2026-10-02T10:40:02.610447301Z"
  }
}
```

- `id` is the Transfer ID, a UUID. `kind` is `upload`, `download`,
  `album_upload`, `recursive_upload`, or `recursive_download`.
- `stage` is `queued`, `hashing`, `uploading`, `downloading`, `publishing`,
  `completed`, `failed`, `cancelled`, or `interrupted`. A Transfer only
  moves forward, and visits the
  stages its kind has: an upload `hashing`, `uploading`, `publishing`; a
  download `downloading`. `completed`, `failed`, `cancelled`, and
  `interrupted` are terminal. `cancelled` ends a Transfer its owner
  cancelled — Ctrl-C on the owning command, or `td transfers cancel` from
  any process. `interrupted` ends one whose owner vanished mid-run: a
  reading process marks it once the Transfer's lease expires, and retrying
  it resumes from saved state.
- `channel` is the drive channel's Telegram ID. `source` and `dest` are the
  two ends of the Transfer: for uploads `source` is the absolute local path
  and `dest` the requested remote path; for downloads the reverse, with
  `dest` the absolute local path. An album upload's `source` is empty — its
  sources are many. Once a single-file Transfer completes, `dest` is the
  path actually written.
- `bytes_done` / `bytes_total` are byte progress, refreshed a few times a
  second while the Transfer runs; only single-file Transfers record them.
  `items_done` / `items_failed` / `items_total` count files: done includes
  skipped, and `items_failed` is omitted when nothing failed. A recursive
  Transfer's `items_total` grows as items report.
- `error_code` / `error_message` appear only on a `failed` Transfer and match
  the error envelope its command reported. A `cancelled` or `interrupted`
  Transfer carries no error.
- `front_end` is the front end that created it (`cli` or `gui`).
- `cancel_requested` is set by `td transfers cancel` or the GUI's Transfers
  tab; the owning process polls it on its lease heartbeat and ends the
  Transfer `cancelled`. A retry clears it when it takes the Transfer over,
  so the recorded request never cancels the new owner.
- Timestamps are RFC 3339 UTC; `finished_at` appears once the Transfer ended.

## Channel list

```json
{
  "ok": true,
  "data": {
    "channels": [
      {"id": 123456789, "title": "Pictures [TD]", "username": "", "invite_link": "https://t.me/+Abc123"}
    ]
  }
}
```

## Scan

```json
{
  "ok": true,
  "data": {
    "mode": "full",
    "channel": "123456789",
    "active": 1240,
    "deleted": 0,
    "invalid": 3,
    "missing": 0
  }
}
```

Incremental scans may include `full_scan_warning`. `--include-deleted` adds `tombstones`. A full scan that continued an interrupted run adds `"resumed": true`.

## List

```json
{
  "ok": true,
  "data": {
    "path": "/Pictures/2024",
    "entries": [
      {"type": "dir", "name": "06", "path": "/Pictures/2024/06"},
      {"type": "file", "name": "beach.jpg", "path": "/Pictures/2024/beach.jpg", "size": 2482911, "hash": "blake3:fullhexvalue"}
    ]
  }
}
```

File entries always carry `hash` — the stored BLAKE3 content hash, `""` when
unknown (rows adopted without `--hash`). Directory entries never carry it.
The field is additive: consumers written against earlier versions keep
working.

The example omits two conditional keys for brevity: file entries of active
rows also carry `"status": "active"`, and directory entries may carry
`"ephemeral": true`.

## Tree

```json
{
  "ok": true,
  "data": {
    "path": "/",
    "tree": [{"type": "dir", "name": "Pictures", "path": "/Pictures", "children": []}]
  }
}
```

## Adopt

Emitted by `td adopt`. ADR 0019 renamed the command from `import`; in the
same release the `imported` counter became `adopted` and the per-item
action `import` became `adopt`.

```json
{
  "ok": true,
  "data": {
    "dry_run": true,
    "adopted": 3,
    "skipped": 1,
    "failed": 0,
    "deleted": 0,
    "items": [
      {"message_id": 61, "kind": "video", "path": "/videos/The Bet.mp4", "action": "adopt", "size": 55113768, "file_name": "The Bet.mp4"},
      {"message_id": 114, "kind": "reply", "action": "delete", "reason": "per-file td-manifest:v1 reply"},
      {"message_id": 3, "kind": "photo", "action": "keep", "grouped_id": 99, "caption": "#tag dump"},
      {"message_id": 3, "kind": "album", "action": "album-manifest", "grouped_id": 99, "reason": "one inventory reply for 6 files"}
    ]
  }
}
```

## Import saved

`td import saved` re-uploads Saved Messages content into the bound drive
channel. It preserves forwarded provenance in `td-origin:v1` comments and
records skipped duplicate captions in `td-dupe:v1` comments.

```json
{
  "ok": true,
  "data": {
    "dry_run": false,
    "source": "saved",
    "into": "/saved",
    "photos_as": "document",
    "history_complete": true,
    "imported": 2,
    "skipped": 1,
    "failed": 0,
    "duplicates": 1,
    "captions_merged": 0,
    "sources_deleted": 0,
    "photos": 1,
    "items": [
      {
        "message_id": 101,
        "kind": "video",
        "action": "import",
        "path": "/saved/Trips/clip.mp4",
        "size": 55113768,
        "hash": "blake3:fullhexvalue",
        "new_message_id": 8821,
        "origin_record_id": 8830
      },
      {
        "message_id": 102,
        "kind": "document",
        "action": "skip",
        "path": "/saved/report.pdf",
        "duplicate_of": "/Archive/report.pdf",
        "reason": "duplicate of /Archive/report.pdf",
        "dupe_record_id": 8831
      }
    ]
  }
}
```

`items` contains one entry for every readable saved message, including
unsupported/service messages skipped during planning. `history_complete=false`
means Telegram ended the read before td could prove that all Saved Messages
were covered; a rerun is required to find the remainder. `--photos-as photo`
uses Telegram's native photo representation and therefore does not promise
byte-for-byte hash identity; `--photos-as document` keeps the downloaded
bytes. In non-interactive or `--json` runs, a photo import without
`--photos-as` is `ERR_USAGE`.

With `--events`, each item is emitted as an `import.item` NDJSON envelope and
the final result is emitted as an `import` envelope. `--dry-run` returns this
same result shape without downloading, uploading, writing records, or
deleting sources.

## Caption cleanup

`td repair --captions [path]` removes the former parent-path and path-derived
`#td_*` scaffold from modern discussion-carrier captions. It never rewrites
legacy `td:v1` caption carriers.

```json
{
  "ok": true,
  "data": {
    "dry_run": false,
    "cleaned": 1,
    "planned": 0,
    "skipped": 2,
    "failed": 0,
    "total": 3,
    "items": [
      {"path": "/stash-browse/832/clip.mp4", "message_id": 8821, "action": "clean"},
      {"path": "/stash-browse/832/other.mp4", "message_id": 8822, "action": "skipped", "reason": "already clean or scaffold not exact"}
    ]
  }
}
```

`--dry-run` uses `"action": "would_clean"` and increments `planned` instead
of `cleaned`. `"message not editable"` and `"empty caption"` are reported as
skips. Without `--continue-on-error`, the command stops and returns the first
unexpected per-message error.

## Recursive download

```json
{
  "ok": true,
  "data": {
    "path": "/Pictures",
    "local": "./restore",
    "downloaded": 10,
    "skipped": 1,
    "failed": 0,
    "errors": []
  }
}
```

## Recursive upload result

```json
{
  "ok": true,
  "data": {
    "uploaded": 42,
    "skipped": 1,
    "failed": 0,
    "errors": [],
    "channel_id": "123456789",
    "invite_link": "https://t.me/+Abc123"
  }
}
```
