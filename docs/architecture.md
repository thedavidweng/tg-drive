# Architecture

`td` has four layers, plus a desktop GUI beside them.

```text
cmd/td
  -> internal/app          CLI wiring and command execution
  -> internal/transfer     Transfer Manager: uploads and downloads as recorded Transfers
  -> internal/service      application use cases
  -> core/*                domain: paths, slugs, captions, errors, ports
  -> adapters/native/*     SQLite, local FS, gotd/td

cmd/td-gui (build tag gui, the only package importing Wails)
  -> internal/gui          facade: Wails services, DTOs, error mapping
  -> internal/service      the same use cases
```

## Desktop GUI (ADR 0031)

- Every GUI Go file carries the `gui` build tag, so default `go build`,
  `go vet`, and `go test` over `./...` never see Wails: the `td` binary and
  the default gates stay CGO-free and need no webview. The GUI is vetted,
  tested, and built with `-tags gui` (`make check-gui`, `make gui-build`).
- `cmd/td-gui` wires the Wails application: it registers the facade
  services and owns every `application.RegisterEvent` call (one file,
  direct init calls with constant names, the only shape the binding
  generator discovers).
- `internal/gui` is a thin facade that never imports Wails, so its tests
  need no display or webview. It holds one service per frontend area
  (Drive, Auth, Transfers, Settings) and only translates: frontend calls to
  `internal/service` calls, results to DTOs, and errors to
  `{code, category, message}` mirroring the JSON contract's error envelope
  (uncategorized errors become `ERR_UNKNOWN`, as the CLI's JSON output maps
  them). It opens the service through `service.Open` with the GUI's own
  session (`gui-session.json` beside the CLI session, ADR 0034).
- The Settings facade covers config get/set (secrets stay redacted unless a
  call explicitly confirms revealing) and Omarchy mode: on a detected
  Omarchy desktop it maps the current theme's `colors.toml` and
  `shell.toml`, plus Hyprland's `decoration:rounding` and
  `general:border_size` parsed from `hyprland.conf` and its `source`d
  files, onto the frontend's design tokens, watches those files, and
  announces changes as the typed `omarchy:theme-changed` event.
- GUI display preferences (theme, language, the Omarchy switch) are stored
  in the webview's local storage, not in td's config file (see the config
  contract).
- The frontend lives in `frontend/` (React, TypeScript, Vite, Tailwind CSS,
  shadcn/ui; Bun as package manager, Node LTS running Vite, pinned in
  `mise.toml`). It calls Go only through the generated bindings in
  `frontend/bindings` (committed, drift-checked by `make
  gui-bindings-check`) and typed events. The `gui`-tagged `frontend` Go
  package embeds the built `frontend/dist`, because `go:embed` cannot reach
  parent directories; `frontend/dist` is built, not committed.
- `go.mod` carries `ignore ./frontend/node_modules` so a stray `*.go` file
  in an npm package can never enter the module's package graph.

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
- `internal/transfer` is the Transfer Manager (ADR 0033). Every `td cp` and
  `td get` form — single-file, album, recursive — submits through it and
  waits. It records each one as a Transfer in the
  `transfers` table, runs at most `transfers.concurrency` at once (the rest
  stay `queued`), turns the call's Observer reports into Transfer stages,
  throttled byte progress, and item counts, and records how it ended. A
  Transfer's call runs
  its own operations inside the service; the Manager takes no locks.
  The owning Manager leases each Transfer from submission, renewing
  `lease_expires_at` on the Operation-lock heartbeat and polling the
  cancel-requested flag on the same tick: `td transfers cancel` sets the
  flag from any process, and the owner cancels the Transfer's context,
  which ends it `cancelled`. Readers mark a Transfer whose lease expired
  `interrupted`. Reading, watching, and cancelling need only the index, so
  those commands open an offline App and never take the Session lock.
  `Watch` follows every process's Transfers by rereading the index on a
  fixed interval and reporting each change. When a Manager starts it prunes
  terminal Transfers that ended more than 30 days ago.
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
| `internal/transfer` | Transfer Manager: submit, run with bounded concurrency, record stages and progress, list, show, and watch Transfers, prune old terminal ones |
| `internal/service` | composition root (`Open`) and use cases: upload (and its dry-run plan), scan, download, move, delete, repair, Telegram setup, config get/set, init channel choices |
| `internal/gui` (`gui` tag) | GUI facade services: DTO translation and error mapping, no business rules |
| `cmd/td-gui` (`gui` tag) | Wails application wiring: service binding, typed event registration, the window |
