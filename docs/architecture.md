# Architecture

`td` has four layers.

```text
cmd/td
  -> internal/app          CLI wiring and command execution
  -> internal/service      application use cases
  -> core/*                domain: paths, slugs, captions, errors, ports
  -> adapters/native/*     SQLite, local FS, gotd/td
```

## Dependency direction

- `cmd/td` imports only `internal/app`.
- `core/*` imports no adapters, `internal/*`, storage drivers, gotd/td, or cobra.
- `internal/app` imports command services and output/error packages.
- `internal/service` owns command behavior and depends on `core` ports, not on `gotd/td`.
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
- `core/telegram/fake` supports integration tests and `TD_FAKE_TELEGRAM=1`.

## Command flow

Example upload:

```text
td cp
  parse flags
  resolve config
  open DB
  resolve channel
  normalize path
  acquire operation locks
  insert pending row
  compute hash/mime
  render manifest/caption
  upload media
  persist message_id on the pending row
  send manifest reply if needed
  commit active DB state
  on any later failure: delete media or mark orphaned
  release locks
  render output
```

## Failure model

- Before Telegram upload: drop the pending row (small files) or leave it for
  resume (big files). A retry to the same destination with matching content
  identity adopts the pending row and sends only unconfirmed parts.
- After Telegram upload: `message_id` is recorded immediately. Publish/index failure deletes the media when possible; otherwise the row is `orphaned` so `td repair --pending` will not upload a second copy.
- After a successful media delete or tombstone: the local row is `deleted` even if the manifest reply cannot be redacted.
- DB write failure after a Telegram edit: run `td scan --full` to reconcile.

## Package responsibilities

| Package | Responsibility |
|---|---|
| `internal/app` | Cobra root, flags, command registration |
| `internal/app/commands` | Command handlers |
| `core/errors` | typed errors and exit code mapping |
| `internal/output` | human/JSON rendering |
| `internal/config` | config/env/path loading and redaction |
| `adapters/native/sqlitestore` | migrations, repositories, File row lifecycle, transactions, locks |
| `core/fsmodel` | canonical paths and virtual tree rules |
| `core/pathcodec` | slug and hashtag generation |
| `core/manifest` | `td:v1`, `td-manifest:v1`, and `td-album:v1` render/parse |
| `core/publisher` | caption/reply + index commit |
| `core/telegram` | interfaces and fake adapter |
| `adapters/native/telegramgotd` | gotd/td adapter |
| `internal/service` | use cases: upload, scan, download, move, delete, repair |
