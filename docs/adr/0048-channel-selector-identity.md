# 0048: Resolve bound drives by identity before title

## Status

Accepted.

## Context

Several independent drive bindings already coexist in the SQLite index.
The CLI and GUI select them through the same service. A query matching either
title or Telegram ID with `LIMIT 1` can route an explicit ID to an earlier
binding whose title happens to equal that ID. Duplicate titles also silently
select whichever row SQLite returns. Both cases can send an operation to the
wrong drive, obstructing the storage identity work tracked in #24.

## Decision

An exact Telegram ID takes precedence over title matches. Without an ID match,
a title must identify exactly one bound drive. An ambiguous title returns the
existing `ERR_USAGE` code (exit 2), directing the caller to an ID in `td status`.
No selector continues to use the first binding.

The existing channel-scoped schema and migration 4 already retain independent
binding identities. This change reads those identities without a migration,
new routing fields, or changes to Telegram manifests.

## Consequences

Numeric titles remain selectable when they do not collide with a bound ID.
Callers using duplicate titles must select an ID instead of relying on row
order. Existing databases, per-channel files and scan checkpoints retain their
identities; logical multi-channel roots and forum topics remain under #24.
