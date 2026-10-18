# 0055: Apply identity selection before binding a remote channel

## Status

Accepted.

## Context

ADR 0048 protects already-bound drives. The native Telegram adapter still
selected the first dialog matching a title or trusted a title-to-ID cache.
That cache only described previously seen channels and could not prove title
uniqueness. A numeric title could also shadow an uncached Telegram ID during
initial binding. The service's resolve-or-create path treated every lookup
failure as permission to create a new channel.

## Decision

Cache access hashes by ID and display titles by ID. Resolve title selectors
against the complete dialog walk, including existing optional ` [TD]` aliases.
An exact ID wins over titles; multiple distinct matching IDs return the
existing `ERR_USAGE`. A failed walk cannot establish title uniqueness.
Explicit cached IDs continue to use their access hashes without a dialog walk.

An absent remote channel returns the existing `ERR_CHANNEL_NOT_FOUND`.
Only that result permits the service's resolve-or-create init path to create
a channel. Ambiguity, cancellation, authentication, rate limits and transport
errors propagate before bindings or linked discussion groups change.

## Consequences

Duplicate titles require an ID from `td channels list`; title lookup may take
more RPCs. Native adapter pagination regressions and service binding tests
exercise this boundary. The fake adapter uses the same identity precedence and
typed errors. No schema, file identity, metadata, migration or forum capability
is introduced; the remaining #24 forum migration must still ship with a
consuming operation and reconstruction tests.
