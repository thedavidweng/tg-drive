# 0054: Select a bound drive by local root from the CLI

## Status

Accepted

## Context

The service can route by persisted local root, but the CLI only exposes
Telegram titles and IDs. Scripts that already own a local root otherwise need
to discover its channel before each command. Titles can change or be duplicated.
An inherited root must also not redirect the recovery scan inside `init`.

## Decision

Expose `--root <local-root>` as a global CLI flag using `WithLocalRoot`.
Resolve relative paths lexically against the current working directory, without
following symlinks. An explicit root takes precedence over `TD_CHANNEL`;
combining it with a nonempty `--channel` is `ERR_FLAG_CONFLICT`.

Use the existing exact binding lookup, unknown-root and ambiguous-root errors.
`status` only reports an uninitialized drive when no explicit selector exists.
An explicit nonempty `WithChannel` overrides an inherited logical root before
an operation starts; operations that already hold locks retain their channel.
This lets `init` scan its own channel regardless of the caller's selection.

## Consequences

Read and write commands can select independent drives with duplicate Telegram
titles using their existing absolute root bindings. No schema, JSON envelope,
Telegram metadata or forum-topic support changes. Existing channel selection
and default-drive behavior remain available.
