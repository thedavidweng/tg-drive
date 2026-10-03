# 0038: GUI channel switches scope calls; one resizable Transfer budget

Status: Accepted.

Context: The GUI facade (ADR 0031) fixed its channel at `service.Open`, so
switching drives reopened the App and rebuilt the Transfer Manager. The
reopen closed the old App under any running Transfer, so a channel switch
stalled every upload and download in flight. The Manager also captured
`transfers.concurrency` as a fixed slot count when it was built, so a
concurrency change made in Settings only applied after a restart, even
though the GUI is a long-lived process that users expect to respond
immediately.

Decision:

- A channel switch only changes the selector `appState` holds. Every facade
  call scopes its context to the selector with `service.WithChannel`, the
  same per-call override the CLI's `--channel` uses, so one App serves
  every channel. A Transfer captures its channel when it is submitted and
  keeps it to the end. Only Auth (setup, login, logout) reopens the App.
- The Manager's queue is bounded by `transfer.Limiter`, a resizable budget
  shared across Manager rebuilds. The Settings facade resizes it when
  `transfers.concurrency` is saved; raising the limit starts queued
  Transfers, lowering it lets running ones finish. The CLI keeps a fixed
  limit per process, because a td command lives only as long as its work.

Consequences:

- Transfers survive drive switches, and the Transfers tab keeps showing
  them; E2E tests in `internal/gui` pin both behaviours.
- Facade methods must resolve the App through `appState.use` and scope
  their context; a call that skips the scope runs on the App's default
  channel. Index sync reports channel-list changes (`channels-changed`)
  instead of the switch path reloading state.
- Every other config key still applies at the next start; the config
  contract documents the one live key.
