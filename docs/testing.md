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
`e2e_events_test.go`, `e2e_observed_output_test.go`,
`e2e_session_lock_test.go`, `e2e_cancel_test.go`, `e2e_transfers_test.go`,
`e2e_transfer_kinds_test.go`, `e2e_transfer_cancel_test.go`,
`e2e_transfer_watch_test.go`, `e2e_transfer_retention_test.go`,
`e2e_transfer_retry_test.go`, `resume_cli_test.go`,
`app_test.go`). They build the real `td` binary and drive the full user
journey — login, init, channels,
cp, ls, tree, get (single and recursive), mv, rm, share, scan, status,
doctor, config get/set, `doctor path-codec`, logout — asserting JSON
envelopes, the `cp --events` NDJSON stream byte for byte, contract exit
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

Observer reports (stages, byte progress, per-item results) of every
long-running use case are pinned against the fake in `internal/service`
(`*_observer_test.go`).

`e2e_cancel_test.go` sends SIGINT during a large `td cp` (after its first
`cp.progress` event), during `td get --recursive` (after its first file
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
  the mapping of a service error to `{code, category, message}` plus
  details, and the auth flow — setup on a credential-less machine, login
  with the code and 2FA prompts answered through the prompt-event seam, a
  rate limit mapping to its wait details, logout, and the GUI and CLI
  holding separate session locks side by side. Two fake knobs cover auth
  paths: `TD_FAKE_AUTH_PASSWORD` gives the account two-step verification,
  and `TD_FAKE_LOGIN_FLOOD_WAIT=<seconds>` fails login with a flood wait
  before any code is sent. The Settings facade is covered by a config
  round-trip with secrets redacted until a confirmed reveal (the service's
  `ERR_CONFIRMATION_REQUIRED` reaches the frontend) and by Omarchy
  detection against a seeded theme and `hyprland.conf`, with a theme
  change emitting on the watch channel. The facade never imports Wails, so
  these tests run headless with no webview or display.
- **Frontend behaviour tests** live beside the screens in `frontend/src`
  and run with `bun test` (Bun's test runner under happy-dom, via
  `@testing-library/react`; Bun is already the package manager, so no
  second test runtime is installed). They render screens with the generated
  bindings replaced by an in-memory backend (`src/testing/memory-backend.ts`)
  and assert user-visible behaviour: the Drive list rendering, an error
  alert, the auth gate (setup on an unconfigured machine, login otherwise),
  the login flow states (code, wrong-code attempts, 2FA password, reused
  and resent codes, rate-limit wait, cancel), logout, and i18n fallback to
  English for an unknown system language; and the Settings tab — config
  keys listed with secrets masked until revealed, edits saved through the
  backend with rejection errors shown, the theme and language overrides,
  and the Omarchy switch applying and clearing the flat theme. The
  in-memory backend's auth fake mirrors the facade's prompt contract,
  including the pending-code reuse a restarted login shows.
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
