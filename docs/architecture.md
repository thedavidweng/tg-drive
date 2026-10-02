# Architecture

`td` has four layers.

```text
cmd/td
  -> internal/app          CLI wiring and command execution
  -> internal/transfer     Transfer Manager: uploads as recorded Transfers
  -> internal/service      application use cases
  -> core/*                domain: paths, slugs, captions, errors, ports
  -> adapters/native/*     SQLite, local FS, gotd/td
```

## Dependency direction

- `cmd/td` imports only `internal/app`.
- `core/*` imports no adapters, `internal/*`, storage drivers, gotd/td, or cobra.
- `internal/app` imports command services and output/error packages.
- `internal/service` owns command behavior and depends on `core` ports, not on `gotd/td`.
- `service.Open` is the one composition root (ADR 0032). It loads config,
  opens the database and the Telegram client (the fake when
  `TD_FAKE_TELEGRAM=1`), wraps either client in the Session lock
  (`adapters/native/sessionlock`, ADR 0034), and seeds cached channel access
  hashes. Front ends
  call it and pass their own diagnostics logger; nothing else opens the
  database or Telegram for a command.
- Product logic a front end would otherwise copy lives in `internal/service`
  too: the upload dry-run plan, Telegram credential setup, config get and
  set (the service decides what is redacted), and the channel choices for
  init. Commands only collect input and render results; interactive answers
  reach the service as values or callbacks.
- `adapters/native/telegramgotd` is the only package that imports `github.com/gotd/td`.
- `adapters/native/sqlitestore` implements the file index, locks, and
  migrations, and owns the File row lifecycle: every `files` status
  transition and path lookup is an intent-named method (`StagePending`,
  `RecordMessage`, `MarkDeleted`, ...) that runs in its own transaction or the
  caller's (ADR 0024). Services do not write `files` SQL.
- `internal/service` changes one file's machine record through the File
  record module (`fileRecord`: `Retire`, `RetireSuperseded`, `Rename`,
  `Rewrite`). The module hides whether the record is a per-file manifest or
  an entry in a shared album inventory (ADR 0028).
- `core/publisher` is the File publisher. Its entry points are intents:
  `Render` + `PublishFile` (new ungrouped file), `Render` + `PublishAlbum`
  (new media group: the `td-album:v1` inventory plus every member row in one
  index transaction), `Move` and `Repair` (existing file's record and
  caption, restored on failure), and `PrepareReindex` + `ReindexBatch` /
  `Reindex` (index only, no Telegram writes). Captions, tags, and slug maps
  come from one `Rendition` that only `Render` produces (ADR 0029).
- `internal/service` runs every Telegram write inside an operation
  (`operate`, `operation.go`). An operation holds the operation locks of the
  canonical paths it touches and carries the channel context, which is
  resolved once per use case; nested operations reuse the locks already
  held (ADR 0030).
- `internal/service` enforces the ADR 0003 confirmation gates and the repair
  mode rules. Each destructive use case takes a typed confirmation on its
  options (`MoveOptions`, `DeleteOptions`, `UploadOptions`, `AdoptOptions`,
  `RepairOptions`, `ImportSavedOptions`) and rejects an unconfirmed call with
  `ERR_CONFIRMATION_REQUIRED`. The options' `Validate` method is the rule;
  commands call it before opening the app context so a refused call opens
  nothing (ADR 0032).
- Cancellation flows through the call's context. `internal/app` installs
  SIGINT and SIGTERM handling on the root command's context; every command
  passes it on. Long-running use cases check it between items
  (`cancelled`, `internal/service/cancel.go`) and fail with
  `ERR_CANCELLED`. Rollback after a cancel, and operation-lock release, run
  on a context that outlives the cancellation, so an interrupted upload
  keeps its resumable state and strands no lock (ADR 0032).
- `internal/transfer` is the Transfer Manager (ADR 0033). A single-file
  `td cp` submits its upload through it and waits. It records each one as a Transfer in the
  `transfers` table, runs at most `transfers.concurrency` at once (the rest
  stay `queued`), turns the call's Observer reports into Transfer stages and
  throttled byte progress, and records how it ended. A Transfer's call runs
  its own operations inside the service; the Manager takes no locks.
  Reading Transfers (`td transfers list` / `show`) needs only the index, so
  those commands open an offline App and never take the Session lock.
  `internal/transfer` depends on `internal/service`, never the reverse, and
  imports no front-end framework.
- `core/telegram/fake` supports integration tests and `TD_FAKE_TELEGRAM=1`.

## Command flow

Example upload. Single-file `td cp`, multi-file and recursive `td cp`, and
`td import saved` all run the one upload pipeline
(`internal/service/upload_pipeline.go`, ADR 0027); a single file is a
one-member run:

```text
td cp
  parse flags
  resolve config
  open DB
  resolve channel
  normalize paths
  plan every member: conflict policy, file/dir invariants (no writes)
  start an operation: acquire operation locks for every destination
  check the discussion group, read thumbnails
  per member: compute hash/mime, resolve the pending row (adopt, supersede,
    or refuse), render caption, stage the pending row
  per send unit (a lone member, or a media group of up to 10 of one kind):
    upload media
    persist message_id on the pending rows
    send the td-album:v1 inventory (groups) or manifest reply (lone member)
    commit active DB state (one transaction for every member of a group)
    retire a --replace target
    on any failure after upload: delete the unit's media or mark orphaned
  end the operation: release locks
  render output
```

## Failure model

- Before Telegram upload: drop the pending row (small files) or leave it for
  resume (big files). A retry to the same destination with matching content
  identity adopts the pending row and sends only unconfirmed parts.
- After Telegram upload: `message_id` is recorded immediately. A record, inventory, or publish/index failure rolls back the whole send unit (single message or media group): media is deleted when possible; otherwise the row is `orphaned` so `td repair --pending` will not upload a second copy, and the command fails with `ERR_ORPHANED_UPLOAD`.
- After a successful media delete or tombstone: the local row is `deleted` even if the manifest reply cannot be redacted.
- DB write failure after a Telegram edit: run `td scan --full` to reconcile.

## Package responsibilities

| Package | Responsibility |
|---|---|
| `internal/app` | Cobra root, flags, command registration |
| `internal/app/commands` | Command handlers |
| `core/errors` | typed errors and exit code mapping |
| `internal/output` | human/JSON rendering |
| `internal/config` | config/env/path loading and redaction rules |
| `adapters/native/sqlitestore` | migrations, repositories, File row lifecycle, transactions, locks |
| `core/fsmodel` | canonical paths and virtual tree rules |
| `core/pathcodec` | slug and hashtag generation |
| `core/manifest` | `td:v1`, `td-manifest:v1`, and `td-album:v1` render/parse |
| `core/publisher` | rendering, manifest/inventory records, index commit |
| `core/telegram` | interfaces and fake adapter |
| `adapters/native/telegramgotd` | gotd/td adapter |
| `internal/transfer` | Transfer Manager: submit, run with bounded concurrency, record stages and progress, list and show Transfers |
| `internal/service` | composition root (`Open`) and use cases: upload (and its dry-run plan), scan, download, move, delete, repair, Telegram setup, config get/set, init channel choices |
