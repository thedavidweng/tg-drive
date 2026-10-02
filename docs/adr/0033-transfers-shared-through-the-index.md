# 0033: Transfers shared through the index, without a daemon

Status: Accepted.

Context: Users want a Transfer started in the CLI to appear in the GUI,
with live progress, and to be cancellable from either side. Codex solves
the same problem with a shared background daemon that every client
attaches to over a socket. It falls back to shared files and a writer lock
when no daemon is running.

Decision: Front ends share Transfers through the SQLite index. They run no
daemon and do not talk to each other directly.

- The Transfer Manager (`internal/transfer`) is used by both the CLI and
  the GUI. It records each Transfer in a `transfers` table with a stable
  Transfer ID, kind, paths, stage, byte progress, error, and its Transfer
  owner. Progress writes are throttled.
- The Transfer owner holds a heartbeat lease on each running Transfer,
  using the same TTL and heartbeat mechanism as Operation locks. A
  Transfer whose lease expires becomes `interrupted`. Retrying it resumes
  from `upload_progress`, and the retrying process becomes its owner.
- Other front ends request cancellation by marking the row. The owner
  notices the mark and cancels.
- The GUI notices changes made by other processes by polling
  `PRAGMA data_version` and reloading only when the database changed.
- A foreground `td cp` or `td get` still owns its Transfers. Ctrl-C
  cancels them, and they end when the process exits.

Considered options:

- A Codex-style daemon owning one Telegram connection. Rejected for now.
  It brings a second frozen protocol contract, version skew between the
  CLI and the GUI, config and `TD_*` environment frozen at daemon start,
  lifecycle handling for auto-start, stale sockets, and idle exit, and a
  second execution path that must produce identical `--json` output.
  Everything this decision builds (the `transfers` table, Transfer
  Manager, per-call observers, Session lock) would be reused by a daemon
  later, so deferring it wastes no work.
- In-memory Transfers inside the GUI only. Rejected: CLI Transfers would
  be invisible, and history would be lost on restart.

Consequences: The storage contract gains the `transfers` table. The CLI
contract gains a `td transfers` command group. There is no `--detach`,
because a CLI Transfer lives only as long as its process. Each front end
keeps its own Telegram connection (ADR 0034).
