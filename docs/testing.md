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
`e2e_session_lock_test.go`, `resume_cli_test.go`, `app_test.go`). They build
the real `td` binary and drive the full user journey — login, init, channels,
cp, ls, tree, get (single and recursive), mv, rm, share, scan, status,
doctor, config get/set, `doctor path-codec`, logout — asserting JSON
envelopes, the `cp --events` NDJSON stream byte for byte, contract exit
codes, human output, `--verbose` diagnostics, the Session lock, and private
file modes against the fake. The stdout of get, scan, every repair mode,
adopt, and import saved is pinned byte for byte, so service observer reports
never leak into CLI output.

Observer reports (stages, byte progress, per-item results) of every
long-running use case are pinned against the fake in `internal/service`
(`*_observer_test.go`).

The Telegram rate-limit middleware (flood waits, transient-error retries) is
below the fake, so its retry bounds are covered by isolated tests in
`adapters/native/telegramgotd/ratelimit_test.go`. The atomic gotd session
storage is below the fake too; `session_test.go` beside it pins that readers
never see a torn session.

The Session lock wraps the fake as well as the real client, so the binary
E2E proves a second process waits and fails with `ERR_SESSION_LOCKED` while
`td ls` runs at once.

## Manual tests

Real-account checks need a Telegram account, `api_id`, and `api_hash`. Use a
private test channel. Sequence and caveats:
[`docs/manual-smoke-tests.md`](manual-smoke-tests.md).
