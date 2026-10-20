# 0057: Existing forum topics as logical roots

## Status

Accepted

## Context

Logical-root selection already preserves a channel row's index, operation
locks and history. A Telegram forum shares one peer across several independent
topics: adding only a schema field would leave uploads, reconstruction, retry
and the desktop peer-ID selector routing to the wrong tree.

## Decision

Extend the persisted identity to account/peer/topic, with 0 reserved for ordinary
bindings. Migrate v6 transactionally without changing any original channel IDs
or child records. Bind only existing, visible/open topics after capability and
history probes. Keep the existing row-scoped operations and pin peer/topic on
each call context; do not maintain a mutable active topic in the client.

Retain conservative edit/delete capability gates for recovered messages.
Permission to send does not establish permission to edit or delete arbitrary
older messages reconstructed from a topic. Recheck topic state at each nested
write operation so a topic closed during a batch stops subsequent mutations.

Use Telegram's topic reply protocol (including General's unflagged semantics)
and existing V1 manifest/album payloads. Topic records are same-topic replies,
not linked broadcast-discussion comments. Filter history emissions, not the raw
pages that establish completeness. Validate media targets before reading or
mutating them. A preview descriptor retains its own topic selection.

Persist the root and topic alongside a Transfer's peer; validate retry identity
before claiming it. The desktop uses explicit root selectors for forum bindings
while preserving peer IDs and ordinary-channel selectors. Add optional topic
fields to binding JSON, not a new envelope or error-code family.

## Consequences

Multiple topics have distinct indexes/locks with no second operation framework.
Ambiguous shared-peer selectors, closed/hidden/missing topics and rebound retry
roots fail explicitly. Forum/topic creation, unscoped legacy conversion and
multi-account switching are not introduced. Schema 7 cannot be downgraded by an
older binary that assumes unique account/peer identity. Offline migration,
protocol and binary fixtures prove the bounded source behavior, not real
Telegram permissions, publication or clean-device distribution.
