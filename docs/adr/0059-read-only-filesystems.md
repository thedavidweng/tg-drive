# 0059: Shared pinned read tree for WebDAV and FUSE

## Status

Accepted

## Context

External filesystem clients need directory metadata and seekable reads without
retargeting requests when the active drive changes. A mount must not treat a
shared forum peer as one filesystem or silently turn writes into downloads.

## Decision

Expose a read-only `io/fs` subtree pinned to the account, channel row, peer,
topic and logical root selected at startup. Refuse a rebound binding. Resolve
only valid relative paths beneath that subtree; reuse active index listings and
Telegram's exact-range preview seam, including native membership checks.
Nonseekable media representations fail explicitly. Each request retains its
cancellation context.

Use the existing x/net WebDAV implementation for GET/HEAD/PROPFIND. Advertise
only OPTIONS/GET/HEAD/PROPFIND and reject every mutation method before dispatch.
The CLI binds only a literal loopback IP, prints its actual listening address
and shuts down on cancellation. Reject non-loopback Host headers to prevent DNS
rebinding and sandbox served content so stored HTML cannot execute in this
endpoint's origin. This is a local read endpoint, without remote
exposure or an authentication service.

Use go-fuse/v2 for Linux/macOS native mounts with the kernel read-only option,
read-only file modes and explicit refusal of write-open flags. Use direct I/O
so an open kernel cache does not conceal later index changes. Direct inode tests
exercise lookup/list/range/read-only behavior without needing a kernel mount.
An installed native FUSE driver and a real mount remain separate acceptance.
Unsupported operating systems report the unsupported platform explicitly.

## Consequences

Both adapters share subtree/topic isolation and range behavior. Read-only
clients can browse and retrieve existing indexed files; no writable mount,
WebDAV locks, background synchronization or remote mutation is implied. The
filesystem-watch command is the explicit content-write workflow. Full scans
rebuild the same index these read adapters consume.
