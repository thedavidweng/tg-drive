# 0029: Publisher intent entry points

## Status

Accepted. Refines ADR 0007; supersedes the `SkipManifestReply` mode of
ADR 0017.

## Context

The File publisher had one `Publish` entry point. Its request carried
mode flags (`EditCaption`, `IgnoreNotEditable`, `SkipManifestReply`,
`OldMeta`, `SetUploadedAt`, a pre-rendered caption with its tags and slug
maps), and every caller picked its own combination. Callers had to keep
one invariant themselves: a pre-rendered caption, its tags, and its slug
maps had to come from the same rendering pass. Album inventories were
written outside the publisher, and each album member was indexed in its
own transaction. A failure on a later member therefore left earlier
members committed, and the rollback removed their rows but left their
derived directories and path tags behind. Scan built its own index
requests next to `Reindex`, so the two copies of the metadata and chain
logic could drift.

## Decision

The publisher exposes one entry point per intent:

- `Render` runs the one rendering pass for a new publication and returns
  an opaque `Rendition`. Uploads need the caption before publication
  because it is sent with the media, so publication is split into a
  prepare step and a commit step instead of rendering inside the commit.
  The caller only reads `Caption()` and `Meta()` from the rendition and
  passes it back unchanged, so the sent caption and the indexed tags
  cannot diverge.
- `PublishFile` completes a new ungrouped file. It sends the per-file
  manifest record and indexes the file as uploaded.
- `PublishAlbum` completes a new media group. It sends the group's one
  `td-album:v1` inventory and indexes every member in one `IndexBatch`
  transaction. The whole group is indexed, or none of it is.
- `Move` and `Repair` rewrite an existing file's record and caption. If a
  Telegram write fails, the record is restored. `Repair` tolerates a
  caption that cannot be edited. `Move` does not.
- `PrepareReindex` with `ReindexBatch`, and `Reindex`, write to the index
  only. Scan prepares each file, records per-file rendering failures as
  scan errors, and commits each chunk with `ReindexBatch`.

`ports.FileIndex` stays the index seam.

## Consequences

- The mode flags and the caller-held same-pass invariant are gone.
  `PublishRequest` no longer exists.
- A database failure while a group is being indexed leaves no member
  indexed. The upload pipeline's crash-window rollback then removes the
  group's media and inventory as before.
- Inventory edits for existing groups (`mv`, `rm`, `repair`) and adopt's
  inventory conversion are still written by the service. They rewrite
  records whose rows already exist, so there is no new group to index.
