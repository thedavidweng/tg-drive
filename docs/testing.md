# Testing

## Local gates

```sh
make test
make test-race
make ci-local          # fmt-check, vet, unit tests, race
```

Unit coverage includes path normalization, slug generation, UTF-16 caption
budgets, JSON envelopes, secret redaction, operation locks, and SQLite
migrations.

## Offline CLI

`TD_FAKE_TELEGRAM=1` runs the CLI against the in-memory fake Telegram client
(no network). Set `TD_FAKE_TELEGRAM_STATE=<path>` to persist login, channels,
and uploaded bytes across invocations.

The fake login code is `12345`. The full command surface — login, init, cp,
ls, get, mv, rm, scan, share — can be exercised this way.

Integration tests under `internal/service` use the same fake for upload,
scan, move, delete, repair, and crash-recovery paths.

Binary-level end-to-end tests live in `internal/app`
(`e2e_lifecycle_test.go`, `e2e_album_test.go`, `e2e_hardening_test.go`,
`e2e_events_test.go`, `e2e_scan_events_test.go`,
`e2e_observed_output_test.go`,
`e2e_session_lock_test.go`, `e2e_cancel_test.go`, `e2e_transfers_test.go`,
`e2e_transfer_kinds_test.go`, `e2e_transfer_cancel_test.go`,
`e2e_transfer_watch_test.go`, `e2e_transfer_retention_test.go`,
`e2e_transfer_retry_test.go`, `e2e_rm_stale_test.go`, `resume_cli_test.go`,
`app_test.go`). They build the real `td` binary and drive the full user
journey — login, init, channels,
cp, ls, tree, get (single and recursive), mv, rm, share, scan, status,
doctor, config get/set, `doctor path-codec`, logout — asserting JSON
envelopes, the `cp --events` NDJSON stream byte for byte, the `scan --events`
stream (stages, per-item tallies, final result, one shared request ID), contract exit
codes, human output, `--verbose` diagnostics, the Session lock, and private
file modes against the fake. The stdout of get, scan, every repair mode,
adopt, and import saved is pinned byte for byte, so service observer reports
never leak into CLI output.

`e2e_transfer_watch_test.go` runs `td transfers watch` in one process
against a slowed `td cp` in another: the watch's `transfer.stage` stream
(under `--events` and under `--json`) must advance with the upload and end
with its terminal stage, and the piped human output must print one line per
observed stage, no more. Both watch forms must exit 130 on Ctrl-C.
`e2e_transfer_retention_test.go` seeds old Transfer rows straight into the
index and asserts a Manager start prunes the terminal ones past 30 days
while keeping recent and active ones.

`e2e_rm_stale_test.go` runs `td rm` with `TD_FAKE_FAIL_COMMENTS=edit` or
`delete`, which fails discussion-thread comment edits or deletes while media
writes succeed. It covers a per-file record delete, a tombstone whose
comment edit falls back to a tombstone caption, an album member whose
inventory rewrite fails, and the last album member whose inventory delete
fails, each with and without `--allow-stale-manifest`. It asserts the exit
code, the `stale_manifest` result or `ERR_TELEGRAM_RPC` details, the index
and Telegram state, and that a full rebuild from the channel agrees.

Observer reports (stages, byte progress, per-item results) of every
long-running use case are pinned against the fake in `internal/service`
(`*_observer_test.go`).

`e2e_cancel_test.go` sends SIGINT during a large `td cp` (after its first
`transfer.progress` event), during `td get --recursive` (after its first file
lands), and at the `td auth login` code prompt. It asserts a prompt exit 130
with `ERR_CANCELLED`, that the command's Transfers end `cancelled`, and that
the next `td cp` resumes from the kept upload
state. `e2e_transfer_cancel_test.go` cancels a running `td cp` from a second
process (`td transfers cancel`, which acts through the index while the
uploader holds the Session lock) and kills one with SIGKILL, whose Transfer
a reader marks `interrupted` once its lease expires.
`e2e_transfer_retry_test.go` then retries those ended Transfers with
`td transfers retry`: the SIGKILLed upload resumes from its saved parts
(the `--events` stream shows only the unconfirmed parts going out), a
cancelled upload and download re-run (the slow retry completing proves the
recorded cancel request was cleared on takeover), failed album and
recursive uploads re-run from their recorded options, and a running or
completed Transfer rejects the retry with `ERR_USAGE` — answered from the
index, fast, while the owning `td cp` runs on. The Transfer lease
reuses `locks.ttl_seconds`, so both tests shorten it with `td config set`
to make the owner's cancel poll and the lease expiry land within the tests'
deadlines. The fake knob `TD_FAKE_TRANSFER_DELAY=<Go duration>` makes every
resumable part and media download take that long, ending early on
cancellation, so the signal lands mid-transfer. Like the other `TD_FAKE_*` knobs (documented on
`fake.NewPersistent`), it is for tests only. The interrupt tests do not run
on Windows, which cannot deliver SIGINT to a child process.

Whether a real gotd media download stops on cancellation is below the
fake, so it is pinned in
`adapters/native/telegramgotd/download_cancel_test.go`.

The Telegram rate-limit middleware (flood waits, transient-error retries) is
below the fake, so its retry bounds are covered by isolated tests in
`adapters/native/telegramgotd/ratelimit_test.go`. The atomic gotd session
storage is below the fake too; `session_test.go` beside it pins that readers
never see a torn session.

The Session lock wraps the fake as well as the real client, so the binary
E2E proves a second process waits and fails with `ERR_SESSION_LOCKED` while
`td ls` runs at once.

## Desktop GUI

GUI tests are not part of the default gates; `mise run check-gui` runs them
(see `docs/release-and-ci.md`).

- **GUI Go tests** live in `internal/gui` behind the `gui` tag. They open
  the facade the way `td-gui` does — `gui.Open` against
  `TD_FAKE_TELEGRAM_STATE` — after seeding a drive through the CLI's own
  service calls, and assert the facade's public methods: the Drive listing,
  tree, mkdir, move, delete, and share; the `{code, category, message}`
  mapping of service errors, including the confirmation-required rejection
  of unconfirmed moves and deletes; the typed scan-progress events a rescan
  emits; and index sync — a second front end's writes trigger a
  `directory-changed` event through the `PRAGMA data_version` poll, which
  also survives an Auth reopen (setup re-pins the poller's connection).
  File preview runs the real facade and media handler under
  `httptest` against the persistent fake: the descriptor of a seekable
  fixture and its HEAD headers, a byte-identical full GET, 206 ranges at
  the start, middle, and end with exact bytes and headers, 416 for an
  unsatisfiable range, a missing or wrong capability refused, a native photo
  served whole as `image/jpeg` without ranges or a length (a Range header
  ignored), an adopted text message served whole as its human text, a
  flood wait answered 429 with `Retry-After` and no file name, path, or
  capability while the listing stays intact, a URL prepared before a channel switch still
  serving the original file, a deleted file's URL answering 404, a range
  near the end of an 8 MiB file reading only that range, and a client
  disconnect cancelling the fake's read. The fake knob
  `TD_FAKE_RANGE_LOG=<file>` appends a JSON line as each range read starts
  and ends (with its error), which is how those tests observe the requested
  ranges and the cancellation; `TD_FAKE_MEDIA_FLOOD_WAIT=<seconds>` makes
  every media body read fail with a flood wait. The gotd adapter's
  largest-photo selection (exact size from `PhotoSize.Size` or the last
  progressive size, otherwise not seekable) is pinned against a fake
  `upload.getFile` invoker.
  The Channels facade is covered by listing bound channels with the active
  one marked, bind choices with bound channels marked, binding an existing
  channel and creating one (explicit and default title) with the result
  activated, rejecting the selection of an unbound channel, the selection
  surviving an Auth reopen, the channel status (file count, discussion
  group, upload limit, last scan, the account's channel permissions) with
  and without a bound channel, linking a discussion group, and a
  `channels-changed` event when a CLI process binds another channel. The
  fake knob `TD_FAKE_DENY_CAPABILITIES=upload,delete,edit,invite` makes
  the capability checks report those permissions missing;
  `TD_FAKE_FAIL_DOCTOR=1` verifies a failed probe leaves the rest of the
  channel status available.
  Auth is covered by setup on a credential-less machine, login with the
  code and 2FA prompts answered through the prompt-event seam, a rate
  limit mapping to its wait details, logout, and the GUI and CLI holding
  separate session locks side by side. Two fake knobs cover auth
  paths: `TD_FAKE_AUTH_PASSWORD` gives the account two-step verification,
  and `TD_FAKE_LOGIN_FLOOD_WAIT=<seconds>` fails login with a flood wait
  before any code is sent. The Settings facade is covered by a config
  round-trip with secrets redacted until a confirmed reveal (the service's
  `ERR_CONFIRMATION_REQUIRED` reaches the frontend), the About versions
  (the stamped GUI version, the probed td CLI version, and empty when no
  td answers), and by Omarchy
  detection against a seeded theme and `hyprland.conf`, with a theme
  change emitting on the watch channel. The Transfers facade is covered by
  the typed event sequences of its upload and download submissions (single
  file, album, recursive), cancel with a shortened `locks.ttl_seconds`,
  retry after a failed resumable upload resuming from saved parts, the
  `ERR_USAGE` rejection of retrying an active or completed Transfer, the
  index-sync pick-up of a transfer a real CLI process started — cancelled
  from the facade — and clear-finished removals; live behaviour across
  the session: a Transfer survives a channel switch mid-upload and
  completes on the channel it started on, and a `transfers.concurrency`
  change in Settings resizes the running queue without a restart; and by
  the option parity with `td cp` / `td get` (video attributes, thumbnail,
  rename policy, upload threads and part size, continue-on-error, and
  their validation errors); and by the option submission: an album upload with presentation and caption, the
  `ERR_CONFIRMATION_REQUIRED` rejection of an unconfirmed replace before
  any Transfer is created, the dry-run plan's parity with the service's
  planner and its per-file upload-limit flags, the download conflict
  policy, and the folder-only guards (presentation and caption rejected
  for directories, replace for albums). Its file dialogs are an
  injected `FilePicker`, scripted in tests and in server mode through
  `TD_GUI_PICK_FILES` / `TD_GUI_PICK_DIR`. The Import facade is covered by
  a dry-run preview of seeded Saved Messages, the run's confirmation gate,
  the delete-source gate on both preview and run, per-item `import.item`
  events with their running tally, duplicate skips, confirmed source
  deletion, and the photo prompt answered or cancelled through the
  prompt-event seam. The Maintenance facade is covered by adopt's preview,
  run, and confirmation gate; all six repair modes (including hash
  backfill against a hash-less adopted file and the orphaned-delete gate);
  doctor's checks and max-upload report; and the path-codec doctor. The
  facade never imports Wails, so these tests run headless with no webview
  or display. The native desktop behaviour's decisions are pure seams in
  `internal/gui/lifecycle.go`, tested the same way: the `TrayTracker`
  folding typed transfer events into the tray menu's model (ordering,
  per-kind progress text, terminal Transfers dropping out) and
  `DecideQuit` / `QuitMessage` deciding when quitting asks; `cmd/td-gui`
  only wires those decisions to Wails' tray, window hooks, and dialog
  manager, which no test can reach without a display.
- **Frontend behaviour tests** live beside the screens in `frontend/src`
  and run with `bun test` (Bun's test runner under happy-dom, via
  `@testing-library/react`; Bun is already the package manager, so no
  second test runtime is installed). They render screens with the generated
  bindings replaced by an in-memory backend (`src/testing/memory-backend.ts`)
  and assert user-visible behaviour: the Drive list rendering, breadcrumb
  navigation, the tree view, new-folder and row-action sheets, confirmation
  sheets blocking destructive actions until confirmed, scan progress
  updating from typed events, the listing refreshing on a
  directory-changed event, an error alert, the channel switcher changing
  the Drive view to the selected channel, the switcher sheet's channel
  status (discussion group, permissions with the missing ones named,
  upload limit, last scan, file count) with link-discussion updating it,
  a channel bound elsewhere appearing on a `channels-changed` event, binding a listed channel and creating one
  with the default title on a fresh machine, the auth gate (setup on an
  unconfigured machine, login otherwise), the login flow states (code,
  wrong-code attempts, 2FA password, reused and resent codes, rate-limit
  wait, cancel), logout, and i18n fallback to English for an unknown
  system language; the Drive tab's upload entry points (file and folder
  pickers, files dropped onto the window) opening the upload options
  sheet — its dry-run plan preview with per-file sizes, over-limit flags
  against the upload limit, replace gated on its confirmation checkbox
  and disallowed for albums, the recursive options shown for folders
  instead of presentation — and its download row actions opening the
  download sheet (local conflict policy, continue-on-error for folders)
  through the folder picker, with the upload sheet's td cp parity fields
  (rename, video attributes, thumbnail, threads, part size); and
  the Transfers tab — stage pills and progress bars updating
  from the typed events, item counts for multi-item transfers, cancel and
  retry calling the backend, failures showing their plain-language reason
  and error code, CLI transfers badged, and clear finished emptying the
  history; and the Settings tab — config keys listed with secrets
  masked until revealed, edits saved through the backend with rejection
  errors shown, the About rows (the CLI row reading "Not installed" when
  no td answers), the theme and language overrides, and the Omarchy switch
  applying and clearing the flat theme; the Import tab — the dry-run plan
  with per-item outcomes, the confirmation sheet blocking the run (with
  the delete-source warning), the photo prompt answered and cancelled, and
  the backend's confirmation gates rejecting unconfirmed calls; and the
  Maintenance tab — adopt's preview and confirmation sheet, repair per
  mode (with the orphaned-delete sheet), and doctor checks rendered as
  pass/warn/fail beside the path-codec rows; and file preview
  (`preview.test.tsx`) — the registry choosing a provider by MIME type,
  then extension, then the fallback; a file name or the Preview row action
  opening the preview while a directory name still navigates; the header's
  name, metadata, Download, and Close; Close and Escape returning focus to
  the opener and leaving the directory as it was, with Escape ignored
  while a viewer is fullscreen and owned by a sheet stacked above; a failed
  preview call or an image that fails to load showing the fallback with
  Download; and the English and Simplified Chinese strings. The in-memory
  backend serves previews from data URLs (`putPreviewForTest`) and
  rejects them on demand (`failPreviewForTest`); the live demo points
  them at its samples' public hosts. `a11y.test.tsx` walks the
  mounted app's interactive controls — exactly the selector set the global
  focus-visible rule styles — asserting every one is keyboard-focusable
  with no positive tabindex, plus the tab strip's arrow-key navigation and
  a sheet's focus trap, Escape close, and focus return to its opener. The
  in-memory backend's auth fake mirrors the facade's prompt contract,
  including the pending-code reuse a restarted login shows; its import and
  maintenance fakes mirror the facade's confirmation and prompt gates.
  Note: `bun:test`'s `expect` thrown inside a `waitFor` callback is not
  retried correctly; assert removals with `waitForElementToBeRemoved`
  instead.
- Frontend type-check (`tsc -b`) and lint (`eslint --max-warnings 0`) are
  gates, run by `make gui-frontend-check`. The bindings in
  `frontend/bindings` are generated and committed; `make
  gui-bindings-check` fails when they drift from the facade.
- **UI preview** (`ui-preview/`) is the GUI's end-to-end artifact: the
  `ui-preview.yml` workflow builds the PR's td-gui in server mode, seeds a
  drive through the CLI against the fake Telegram, and records scripted
  scenes with Playwright — one 2x screenshot per scene plus a walkthrough
  mp4, published into a marked block in the PR description. The same
  `ui-preview/run.sh` reproduces a preview locally, so every run yields a
  verifiable repeatable artifact. See "UI preview" in
  `docs/release-and-ci.md`.

## Manual tests

Real-account checks need a Telegram account, `api_id`, and `api_hash`. Use a
private test channel. Sequence and caveats:
[`docs/manual-smoke-tests.md`](manual-smoke-tests.md).
