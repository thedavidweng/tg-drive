# 0032: Front-end-neutral service layer

Status: Accepted.

Context: Some product rules live only in `internal/app/commands`. These
include the ADR 0003 confirmation gates, the ADR 0006 channel picker data,
the `cp --dry-run` plan, Telegram config setup, and config read/write. A
second front end would have to copy them. `service.App` is opened by a
private, Cobra-bound `OpenApp`. It also carries per-call state as shared
fields. `initScan` temporarily rewrites `App.Channel`, every upload shares
the single `App.Progress` callback, and the CLI mutates `App.Cfg` to apply
upload overrides. A long-lived process running calls concurrently would
race on all three. Downloads, scans, adopt, and repair report no progress.
Recursive loops do not check cancellation between items, and the CLI never
cancels at all.

Decision: `internal/service` serves every front end the same way.

- `service.Open(Options)` is the one composition root for config, the
  database, and the Telegram client. The CLI and the GUI both use it.
- Confirmation gates are typed fields on the use-case options. A
  destructive call without confirmation is rejected by the service itself
  with the existing error codes. Front ends decide only how to ask.
- Channel choice, upload overrides, and progress reporting are per-call
  options. `App` holds no state that a call mutates.
- Long-running use cases accept an observer that receives stage changes and
  byte progress. They check the context between items, so cancellation
  stops them promptly. The CLI cancels on SIGINT and SIGTERM. A cancelled
  call fails with the new code `ERR_CANCELLED` (category `cancelled`,
  retryable), which the CLI maps to exit code 130, the shell convention
  for an interrupted command. No existing code fits: the generic codes
  (`ERR_TELEGRAM_RPC`, `ERR_DB`) would tell a script that Telegram or the
  index failed when the user stopped the command.

Consequences: Existing CLI commands, flags, JSON output, and exit codes
are unchanged, apart from the new `ERR_CANCELLED` and exit code 130.
Commands become thin translators from flags to options and from results
to output. Interactive prompts stay in front ends; the
service receives answers through callbacks or options.
