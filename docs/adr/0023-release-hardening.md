# 0023: Release hardening of diagnostics, privacy, and retries

Status: Accepted

Context: An audit of the original V1 product spec against the shipped CLI
found gaps a first-time user hits: `--verbose`/`TD_VERBOSE` did nothing,
`td status` failed before `td init`, the SQLite index was created
world-readable, transient Telegram server errors failed the command on the
first attempt, `td config get --show-secrets` ignored `--confirm` in text
mode and silently redacted when stdin was not a terminal, and the spec's
`td doctor edit-window|upload-limit|hashtag-scope` subcommands never existed.

Decision:

- `--verbose` / `TD_VERBOSE=1` write `debug:` lines to stderr: resolved
  config, DB, and session paths (home abbreviated to `~`), connection setup,
  and per-RPC method, latency, error type, retries, and flood waits. They
  never include credentials, phone numbers, message text, or file names.
- Config, session, and DB files (with `-wal`/`-shm`) are owner-only: mode
  0600/0700 on POSIX, a protected DACL granting only the current user on
  Windows. The DB is re-tightened on every open. This promotes
  `golang.org/x/sys` (already in the module graph through gotd) to a direct
  dependency. `td doctor` reports a `file_permissions` check.
- Transient RPC failures (5xx, MTProto timeouts) are retried up to three
  times with exponential backoff from 0.5s, for idempotent methods only:
  history/message reads, channel and user lookups, file downloads, and file
  part uploads. Sends, edits, deletes, and channel creation are never
  replayed, because a server error does not prove the mutation was not
  applied and a replay could duplicate a post.
- `td status` succeeds before `td init` with `initialized: false` (exit 0,
  previously `ERR_CHANNEL_NOT_FOUND`/exit 4). An explicit `--channel` that
  matches nothing still fails. Status lists every bound channel with its
  local root. Human output abbreviates the home directory; JSON keeps
  absolute paths for scripts.
- `--show-secrets` without `--confirm` fails with
  `ERR_CONFIRMATION_REQUIRED` (exit 10) whenever no human can answer the
  prompt: JSON mode (previously `ERR_USAGE`) or a non-terminal stdin.
- `td doctor` adds `db_wal`, `history_read` (a one-message history page),
  and `file_permissions`. A free-tier upload limit is `pass`, not `warn`.
  The spec's doctor subcommands stay out: upload limit and edit permission
  are already checks in `td doctor`, hashtag scope no longer applies to new
  captions (ADR 0021), and an edit-window probe would have to edit a real
  user message.
- `td init` scans the channel it just bound (not the first bound channel)
  and reports `indexed_files`, or `scan_error` when the scan fails, so
  binding an existing drive is the documented index-rebuild path.
- Channel listing and the init picker exclude supergroups, including td's
  own discussion groups, which can never be a drive.

Consequences: Scripts that relied on `td status` failing before init, or on
`ERR_USAGE` for `--json --show-secrets`, must switch to `data.initialized`
and `ERR_CONFIRMATION_REQUIRED`. The retry policy lives in the rate-limit
middleware, which the fake client does not exercise, so it keeps isolated
tests for its bounds (ADR 0022).
