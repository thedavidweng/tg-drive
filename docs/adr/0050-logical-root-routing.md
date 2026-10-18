# 0050: Route operations by logical local root

## Status

Accepted

## Context

One SQLite index can contain multiple independent drive bindings. Telegram
titles are mutable and can be duplicated, while a caller that already owns a
local root needs to select its corresponding storage channel without relying
on a display name. The channel row already persists the absolute
`root_local_path` used when the binding is created.

## Decision

The service exposes `WithLocalRoot(ctx, localRoot)` as an explicit logical-root
selector. It resolves the exact `channels.root_local_path` row before an
operation starts, then carries the same channel context through the operation
and its path locks. An unknown root returns `ERR_CHANNEL_NOT_FOUND`; duplicate
rows return `ERR_USAGE` instead of selecting by row order. `WithChannel` remains
the CLI/GUI selector for Telegram ID or title. ADR 0054 extends this selector to
override an inherited root when selecting a newly bound channel explicitly.

This slice deliberately reuses the existing binding identity. It adds no
forum-topic columns or unused routing fields. Forum topics require a separate
Telegram capability and manifest-routing contract before they can be selected.

## Consequences

Operations can route a local root to its own channel without title collisions,
and all existing channel-scoped indexes and locks remain unchanged. Existing
databases need no migration. Callers must pass the same absolute local-root
path recorded by `td init`; a future user-facing root registry can build on
this identity without changing storage reconstruction.
