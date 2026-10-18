# 0051: Persist directory delete progress

## Status

Accepted

## Context

Directory deletion commits each file separately because Telegram cannot
participate in a transaction with the local index. Returning counters only
to the caller loses the operation history after the command exits. The
remaining count also excluded a failed active file, understating retry work.

## Decision

Create one local progress row before each directory delete starts its remote
mutations. Return its UUID as `operation_id` in directory results and error
details. Persist counters and the last attempted file after each iteration,
and record completed or failed status when the operation exits normally.
An interrupted process leaves its last recorded running state.

Count failed attempts separately from the active files remaining, including
unattempted files. Retrying the directory uses its current active index and
creates a new operation ID, preserving the earlier partial result.

## Consequences

Schema version 5 adds local operation history without changing Telegram
metadata or single-file results. The progress row describes the latest local
checkpoint; a process interruption between a remote write and its index commit
still requires the existing repair protocol. It does not claim atomic batch
deletion or automatic resume.
