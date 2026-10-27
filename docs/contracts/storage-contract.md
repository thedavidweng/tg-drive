# Storage contract

SQLite is a cache and operation index. Telegram messages are the recoverable source.

## Required pragmas

```sql
pragma foreign_keys = ON;
pragma journal_mode = WAL;
pragma busy_timeout = 5000;
```

The pragmas ride in the connection DSN so **every pooled connection** enforces
them (executing them once on a single connection leaves foreign keys off on
the rest of the pool). Write transactions begin `IMMEDIATE`, so concurrent
processes queue on the busy timeout instead of failing mid-transaction on a
deferred-to-write upgrade. The connection pool is capped (4 connections) — a
CLI needs a handful of concurrent statements and WAL allows one writer.

## Versioned migrations

`schema_version` records every applied migration; each migration runs in one
transaction together with its version row, so a database can never be left
half-migrated.

The baseline (version 1) creates the core index idempotently. Earlier local
pre-release databases may already record versions 2 and 3 from before those
numbers were assigned to the Transfer schema. Opening them preserves their
channel bindings and indexed files while completing the missing Transfer
schema in migration 4 (ADR 0039). Do not delete the database merely because
the Transfer table is missing.

Version 2 adds the `transfers` table to databases already at version 1.
Version 3 adds `items_failed` to it.
Version 4 converges pre-Transfer databases with colliding version numbers
and current databases, creating the table and missing column only when absent.
Version 5 adds `directory_delete_operations`, which records durable counters
and the last attempted path for each directory delete. Opening a database at
version 4 preserves all existing bindings, files, and transfer state while
adding this progress table.
Version 6 adds `directory_move_operations`, preserving existing bindings,
indexed files, transfers and directory-delete history.
Version 7 rebuilds `channels` with `topic_id` and uniqueness on
`(account_id, tg_channel_id, topic_id)`. Existing bindings retain their IDs,
all original columns and `topic_id=0`; files, nodes, transfers, upload state,
scan checkpoints and directory histories keep their original references.
Foreign-key enforcement is disabled on the migration's dedicated connection
before BEGIN, integrity is checked before COMMIT, and enforcement is restored
afterwards. A failed integrity check rolls back the schema and version row.
All migration steps and their version records run in a transaction.
Version 8 adds `watch_checkpoints`, preserving all existing rows. Each completed
watched file records its BLAKE3 hash and size under the channel row, absolute
local directory, canonical remote directory and relative path. These local
ownership checkpoints authorize optional deletion; a full Telegram scan
recovers file content but cannot recreate watch deletion ownership.

## Schema

```sql
create table schema_version (
  version integer primary key,
  applied_at text not null
);

create table accounts (
  id integer primary key,
  tg_user_id text not null unique,
  phone text,
  display_name text,
  created_at text not null,
  updated_at text not null
);

create table channels (
  id integer primary key,
  account_id integer not null references accounts(id),
  tg_channel_id text not null,
  topic_id integer not null default 0 check(topic_id >= 0),
  access_hash text,
  title text not null,
  username text,
  invite_link text,
  root_local_path text not null,
  root_remote_path text not null default '/',
  strategy text not null default 'single',
  discussion_tg_channel_id text not null default '',
  discussion_access_hash text not null default '',
  discussion_title text not null default '',
  created_at text not null,
  updated_at text not null,
  unique(account_id, tg_channel_id, topic_id)
);

create table nodes (
  id integer primary key,
  channel_id integer not null references channels(id),
  canonical_path text not null,
  parent_path text,
  display_name text not null,
  type text not null check(type in ('dir', 'file')),
  derived integer not null default 1,
  ephemeral integer not null default 0,
  created_at text not null,
  updated_at text not null,
  unique(channel_id, canonical_path)
);

create table files (
  id integer primary key,
  channel_id integer not null references channels(id),
  node_id integer references nodes(id) on delete set null,
  message_id integer,
  manifest_message_id integer,
  canonical_path text not null,
  display_name text not null,
  manifest_chat_tg_id text not null default '',
  content_hash text,
  mime text,
  caption_version integer not null default 1,
  status text not null check(status in ('pending', 'active', 'deleted', 'superseded', 'missing', 'invalid', 'orphaned')),
  uploaded_at text,
  updated_at text not null
);

create unique index idx_files_active_path
  on files(channel_id, canonical_path)
  where status = 'active';

create unique index idx_files_pending_path
  on files(channel_id, canonical_path)
  where status = 'pending';

create unique index idx_files_channel_message
  on files(channel_id, message_id)
  where message_id is not null;

create index idx_nodes_channel_parent on nodes(channel_id, parent_path);
create index idx_nodes_channel_type on nodes(channel_id, type);
create index idx_files_channel_status on files(channel_id, status);
create index idx_files_channel_path on files(channel_id, canonical_path);
create index idx_path_tags_tag on path_tags(tag);
create index idx_scan_errors_status on scan_errors(channel_id, status);

create table path_segment_slugs (
  id integer primary key,
  channel_id integer not null references channels(id),
  parent_canonical_path text not null,
  segment text not null,
  slug text not null,
  hash_len integer not null,
  created_at text not null,
  unique(channel_id, parent_canonical_path, segment),
  unique(channel_id, parent_canonical_path, slug)
);

create table path_tags (
  id integer primary key,
  file_id integer not null references files(id) on delete cascade,
  tag text not null,
  depth integer not null,
  unique(file_id, tag)
);

create table operation_locks (
  key text primary key,
  owner_token text not null,
  acquired_at text not null,
  expires_at text not null
);

create table scan_state (
  id integer primary key,
  channel_id integer not null references channels(id),
  last_scanned_message_id integer,
  last_full_scan_at text,
  discussion_last_scanned_message_id integer,
  full_scan_started_at text,
  updated_at text not null,
  unique(channel_id)
);

create table scan_errors (
  id integer primary key,
  channel_id integer not null references channels(id),
  message_id integer,
  error_code text not null,
  error_message text not null,
  raw_excerpt text,
  status text not null check(status in ('pending', 'resolved')),
  first_seen_at text not null,
  last_seen_at text not null,
  resolved_at text,
  unique(channel_id, message_id, error_code)
);

create table upload_progress (
  key text primary key,
  file_id integer not null references files(id) on delete cascade,
  telegram_file_id integer not null default 0,
  content_hash text,
  part_size integer not null,
  total_parts integer not null,
  total_bytes integer not null,
  confirmed_parts text not null,
  confirmed_bytes integer not null default 0,
  updated_at text not null
);

create table transfers (
  id text primary key,
  kind text not null,
  channel_tg_id text not null,
  source text not null,
  dest text not null,
  options text not null default '{}',
  stage text not null,
  bytes_done integer not null default 0,
  bytes_total integer not null default 0,
  items_done integer not null default 0,
  items_total integer not null default 0,
  items_failed integer not null default 0,
  error_code text not null default '',
  error_message text not null default '',
  front_end text not null,
  owner_token text not null default '',
  lease_expires_at text not null default '',
  cancel_requested integer not null default 0,
  created_at text not null,
  updated_at text not null,
  finished_at text not null default ''
);

create index idx_transfers_stage on transfers(stage);
create index idx_transfers_created on transfers(created_at);

create table directory_delete_operations (
  id text primary key,
  channel_id integer not null references channels(id),
  path text not null,
  mode text not null,
  total integer not null,
  deleted integer not null default 0,
  failed integer not null default 0,
  remaining integer not null,
  status text not null check(status in ('running', 'completed', 'failed')),
  last_path text not null default '',
  error text not null default '',
  created_at text not null,
  updated_at text not null
);

create index idx_directory_delete_operations_channel
  on directory_delete_operations(channel_id, updated_at);

create table directory_move_operations (
  id text primary key,
  channel_id integer not null references channels(id),
  source_path text not null,
  requested_destination text not null,
  destination_path text not null,
  total integer not null,
  moved integer not null default 0,
  failed integer not null default 0,
  remaining integer not null,
  status text not null check(status in ('running', 'completed', 'failed')),
  last_path text not null default '',
  error text not null default '',
  created_at text not null,
  updated_at text not null
);

create index idx_directory_move_operations_channel
  on directory_move_operations(channel_id, updated_at);
```

Schema 8 also creates:

```sql
create table watch_checkpoints (
  channel_id integer not null references channels(id),
  local_root text not null,
  remote_root text not null,
  relative_path text not null,
  content_hash text not null,
  size integer not null,
  primary key(channel_id, local_root, remote_root, relative_path)
);
```

## Logical-root routing

`channels.root_local_path` is the logical-root identity for a binding. It is
stored as the absolute path resolved by `td init` and maps one local root to
one storage channel row; all channel-scoped nodes, files, slugs, scan state and
operation locks retain that row's identity.

The service selector `WithLocalRoot(ctx, localRoot)` resolves an exact
`root_local_path` before an operation starts. An unknown path returns
`ERR_CHANNEL_NOT_FOUND`; duplicate rows return `ERR_USAGE` rather than routing
by row order. Schema 7 binds an existing forum topic by positive topic ID;
0 identifies the ordinary-channel binding. Different topics under one peer
have distinct channel rows, root paths, indexes, locks and scan checkpoints.
Selecting their shared peer ID or title is ambiguous and returns `ERR_USAGE`;
use `--root` instead. `td init --bind-channel=<peer> --topic <id>` verifies the
peer is a forum, the existing topic is visible/open, required upload/edit/delete
capabilities are available and a history request succeeds before binding.
It does not create forums/topics. An unscoped legacy binding of the same forum
must be resolved explicitly, not silently converted.

Telegram routing is operation-scoped, never mutable global client state.
New messages/albums target the topic's root message; subsequent machine-record
replies carry `top_msg_id` when needed. General (ID 1) uses Telegram's unflagged
message semantics. Manifest/album records remain the existing V1 format, carried
by same-topic replies rather than a broadcast linked discussion group. Reads,
preview/range bodies and mutation targets verify topic membership; full scan
filters emitted records while retaining raw-page pagination/completeness proof.
Cross-topic manifest references cannot resolve a media body. Closed/hidden or
missing topics reject operations before remote writes. No authenticated Telegram
acceptance is implied by offline fixtures.

The CLI `--root` supplies this selector after making relative paths absolute.
An explicit nonempty `WithChannel` selects its channel even when the incoming
context carries a root, so binding recovery scans the channel just bound.
An operation that already owns locks retains its resolved channel.

## Transfers

`transfers` records every Transfer (ADR 0033), one row per Transfer, written
by the Transfer Manager (`internal/transfer`) of the process that owns it
and read by any front end. Rows are local bookkeeping: they are not
reconstructed from Telegram, and `td scan --full` neither reads nor clears
them.

- `id` is a random (version 4) UUID, the Transfer ID.
- `kind` is `upload` (one local file), `download` (one remote file),
  `album_upload` (several local files as Telegram albums, one Transfer for
  the call), `recursive_upload` (one local directory tree), or
  `recursive_download` (one remote directory tree).
- `channel_tg_id` is the Telegram ID of the drive channel, resolved when the
  Transfer is submitted; empty when none resolves, and the Transfer then
  fails with the call's own error.
- `source` / `dest` are the two ends of the Transfer: for uploads `source`
  is the absolute local path and `dest` the requested remote path; for
  downloads the reverse, with `dest` the absolute local path. An album
  upload's `source` is empty (`options.sources` lists its files). Once a
  single-file Transfer completes, `dest` is the path actually written.
- `options` is a JSON object with what a retry needs besides the paths:
  `policy` (`fail`, `replace`, `skip`, `rename`), and for uploads
  `no_hash`, the presentation (`kind`, `duration_seconds`, `width`,
  `height`, `supports_streaming`, `thumb_path`), `threads`, `part_size_kb`,
  `confirm_replace`, and `caption`; an album upload adds `sources`;
  recursive Transfers add `continue_on_error`, and a recursive upload
  `include_empty_dirs`. Unset and zero values are omitted.
- A single-file Transfer's `stage` moves forward only: `queued`, `hashing`, `uploading` /
  `downloading`, `publishing`, then one terminal stage: `completed`,
  `failed`, `cancelled`, or `interrupted`. A Transfer visits the stages its
  kind has. An album or recursive Transfer reports its current group's
  work, so it can re-enter a working stage for a later group; terminal
  stages cannot be re-entered. A Transfer cancelled by its owner — Ctrl-C on the owning
  command, or a cancel request from another process — ends `cancelled`.
  `interrupted` ends a Transfer whose owner vanished mid-run: see
  `owner_token` / `lease_expires_at` below.
- `bytes_done` / `bytes_total` are byte progress of a single-file Transfer;
  `bytes_total` starts as the source file's size, and multi-item Transfers
  count items instead and leave both at 0. Stage changes are written at
  once; byte progress is written at most four times a second per Transfer.
- `items_done` / `items_failed` / `items_total` count the files a Transfer
  moves: done counts completed and skipped items. A single-file Transfer is
  1 item; an album upload knows its `items_total` when submitted; a
  recursive Transfer's `items_total` grows as items report. A completed
  Transfer has `items_done` = `items_total` − `items_failed`.
- `error_code` / `error_message` are set when it ends `failed`: the same
  code and message the creating command reported. A `cancelled` or
  `interrupted` Transfer leaves them empty.
- `front_end` is the creating front end: `cli` or `gui`.
- `owner_token` identifies the owning process's Transfer Manager. The owner
  leases the Transfer from submission: it writes `lease_expires_at` and
  renews it on the Operation-lock heartbeat (the `locks.ttl_seconds` TTL,
  renewed at one third of it) until the Transfer ends. Any process that
  reads a non-terminal Transfer whose lease expired — an empty lease counts
  as expired — marks it `interrupted` with `updated_at` and `finished_at`
  set. The reader claims nothing: `owner_token` stays for a retry to take
  over. The marking compares and swaps on the lease value read, so a
  renewal that landed meanwhile turns it into a no-op.
- A retry takes over with one conditional write: only a row in `failed`,
  `cancelled`, or `interrupted` can be claimed, so a running Transfer —
  whose fresh lease keeps it active — and a completed one reject the claim,
  as does a row a concurrent retry claimed first. The claim writes the new
  owner's `owner_token` and a fresh `lease_expires_at`, moves `stage` back
  to `queued`, clears `finished_at`, `error_code`, `error_message`, and
  `cancel_requested` (a recorded cancel request must not cancel the new
  owner's first heartbeat), and zeroes `bytes_done` and the item counts for
  the new run. `id` and `created_at` stay: a retry is the same Transfer's
  history continuing, not a new one.
- `cancel_requested` is set by `td transfers cancel` from any process. The
  owner polls it on the same heartbeat and cancels the Transfer's context,
  which ends the Transfer `cancelled`; a `queued` Transfer is cancelled
  without starting. The flag stays set on the ended Transfer until a retry
  clears it.
- Timestamps are fixed-width UTC text
  (`2006-01-02T15:04:05.000000000Z`), so they sort in time order;
  `finished_at` is empty until the Transfer ends.
- Retention: when a Transfer Manager starts (any command that submits or
  lists Transfers starts one), it deletes the rows in a terminal stage
  whose `finished_at` is more than 30 days old. Active Transfers and newer
  history are kept.

## Directory delete progress

`directory_delete_operations` records each directory `td rm` as a separate
local operation. The command returns its UUID as `operation_id`; the row is
created before the first Telegram mutation and updated after each indexed
file. `status` is `running`, `completed`, or `failed`. `deleted`, `failed`,
and `remaining` are durable indexed-file counters, while `last_path` and
`error` identify the most recent attempt. A retry creates a new operation row
and never overwrites the previous partial result.

## Directory move progress

Directory move and delete terminal checkpoints run while the operation still
holds its locks, with a separate local context limited to five seconds. A
cancelled Telegram call therefore records `failed` and its last attempted
path/counters without replacing `ERR_CANCELLED` with a cancelled SQL write.
Remote writes always use the original cancellable context. A checkpoint write
failure remains an error. Process termination can still leave `running` records;
these checkpoints do not implement crash replay.

`directory_move_operations` records a UUID, channel, source, requested and
resolved destination, total/moved/failed/remaining counters, status, last
attempted source path and error. Its row is created under the path locks
before remote writes; checkpoints precede each attempt and follow each
successful rename. Status is `running`, `completed`, or `failed`.

A failed directory move includes `operation_id` and counters in its error
details. Retrying the same source and requested destination in the same
channel uses the latest unfinished operation's resolved destination. This
prevents a directory created by the partial move from changing destination
interpretation on retry. Destinations are still validated before mutation.
Each retry creates a separate history row. A completed operation does not
affect the interpretation of a new move.

After all indexed files move, remaining local directory nodes move in one
transaction within the held source/destination locks. Empty subtrees use the
same operation history with zero file counters. New local destination nodes
are ephemeral; existing destination nodes retain their GC state, as with
explicit folder creation. Missing destination ancestors are created locally. A remote failure
leaves these folders at the source. A local transaction failure rolls back
its node changes and records failed history; retry reuses the resolved
destination even when all files already moved. The operation's counters
continue to count indexed files, not local folders.

Process interruption can leave a running checkpoint. The history is local;
it does not make Telegram and SQLite atomic or replace repair after a crash
between remote and index writes.

## Operation locks

Use one namespace for all path-touching operations:

```text
path:<channel_id>:<canonical_path>
```

Acquisition is a single atomic conditional write: the winner inserts or takes
over a stale row (`expires_at < now`) with its own `owner_token`; lock times are
stored as UTC RFC3339Nano text so short configured TTLs do not lose sub-second
lease precision. The loser receives the typed `ERR_OPERATION_LOCKED` error,
never a raw database-busy failure. A process may release only locks with its
own `owner_token`.

Long operations (uploads, moves, deletes, imports, album rewrites) hold their
locks with **heartbeat renewal**: the holder renews at one third of the TTL
for the duration of the work, so an operation legitimately longer than the
TTL still excludes concurrent mutators. If renewal discovers the lock was
taken over, the operation aborts instead of continuing unprotected. Locks
are released on a background context, so cancelling a command (Ctrl-C) cannot
strand a path for the remaining TTL.

## Session file and Session lock

The Telegram session file (`storage.session_path`) is replaced atomically:
the new session is written to a temp file in the same directory, synced, made
owner-only, and renamed over the old one, so a crash leaves either the old or
the new session, never a partial one.

The Session lock is an exclusive OS file lock (`flock` on Unix, `LockFileEx`
on Windows) on `<session_path>.lock`. A process takes it on its first
Telegram call and holds it until it closes the client; the lock file itself
is never deleted. A process that finds it held polls until
`locks.session_wait_seconds` passes, then fails with `ERR_SESSION_LOCKED`.
Commands that read only the local index never take it. Each front end
configures its own session path, so the CLI and another front end do not
share one lock.

The desktop GUI's session is `gui-session.json` in the directory of the
resolved CLI session path (so with the defaults,
`~/.config/tg-drive/gui-session.json`, or the previous `tg-drive-cli`
directory when that install is still in use). It is not configurable. The GUI
logs in on it independently and appears as its own device in Telegram (ADR
0034): its device model is `td-gui`, while the CLI keeps the gotd default
identity.

## Directory GC

When a file leaves `active`, clear `files.node_id` in the same transaction. Then remove derived directory nodes that have no active descendants. Directory GC runs as a single transaction.

Directories created explicitly by a front end (the GUI's new-folder action) are stored as `derived=1, ephemeral=1` nodes: ephemeral nodes are local-only — Telegram cannot store an empty directory — so directory GC keeps them and a full scan's node rebuild removes them unless a file was uploaded into them.

An explicit directory delete removes empty directory nodes in its selected
channel and subtree, including ephemeral nodes, after the batch has no
remaining files. A partial failure retains those local nodes until retry.
The cleanup is transactional and preserves nodes with active descendants.
Deleting `/` applies this cleanup to every empty directory in that channel.
Result counters count files only; removing an empty local directory adds no
deleted file count or Telegram metadata.

## Machine record carrier

Machine records (`td-manifest:v1` per ungrouped file, `td-album:v1` per
album) in ordinary channels live in the comment thread of the file's post inside the channel's
linked discussion group (ADR 0018). Import annotations (`td-origin:v1` and
`td-dupe:v1`) use the same carrier. Media captions carry human text only.
Modern media captions contain the caller's human text and display name only;
td does not render the remote parent path or path-derived `#td_*` tags into
them. The full path, hash, MIME, and tag chain remain in the discussion
manifest. `td repair --captions` removes that former scaffold from existing
modern captions without touching legacy `td:v1` carriers.
Telegram creates comment threads only for posts sent **after** the
discussion group was linked; on the first record write for an older post the
adapter bootstraps a thread by forwarding the post into the group (the
manual forward carries `fwd_from.channel_post` like the auto-forward). If
forwarding is impossible (protected content), the record falls back to the
legacy in-channel reply carrier. Rows record the
carrier peer in `files.manifest_chat_tg_id`: empty means the legacy
in-channel reply carrier, which remains first-class and is parsed forever.
Every ordinary-channel machine-record write (upload, mv, rm, import, repair) requires a
linked discussion group; reads and scans work on legacy channels.
`td channels link-discussion` creates and links one; `td doctor` reports it.
Forum roots instead use replies within their selected topic and never require
or create a linked discussion group (ADR 0057).

`td-origin:v1` is an additive provenance record for content republished by
`td import saved`. It names the source (`src=saved`), source message
(`smid`), forwarded origin id/title/post/date when available, import date,
and either the imported canonical path (`p`, for a single file) or album
grouped id (`g`). `td-dupe:v1` is an additive record for a saved item skipped
because its BLAKE3 hash already exists. It names the matched path and hash
and stores the skipped item's caption, truncating only that caption when the
Telegram text budget requires it (`capcut=1`). These records are
non-authoritative: losing one removes provenance or caption history, not the
file or its path.

Telegram is the source of truth, and contradictory Telegram state resolves in
a fixed order during `td scan --full`:

1. A caption tombstone (`td:v1 deleted=true`) is sticky and wins over every
   other record of its message — including a newer live comment. The
   tombstone delete falls back to a caption tombstone when the comment edit
   fails, so deletion cannot be undone by the stale thread record.
2. Otherwise a comment record wins over caption metadata, which wins over
   the legacy in-channel reply, whatever the older carrier still claims.
3. A missing or corrupt `td-album:v1` inventory — comment or legacy reply —
   is a scan error (`ERR_ALBUM_INVENTORY_INVALID`), parity with per-file
   manifests: the whole album can never silently vanish from the index.
4. Duplicate claims on one path resolve newest-message-wins; the older
   duplicate is recorded as a scan error (also within one commit chunk).

Slug assignment during scans is deterministic (message-id order — the same
chronological order uploads are assigned in), so a rebuilt index reproduces
the original tag chains. Tags remain index and manifest data; modern captions
do not depend on them.

Full scans walk the drive channel and the discussion group. Each peer has
its own completeness proof and cursor (`scan_state.last_scanned_message_id`,
`scan_state.discussion_last_scanned_message_id`); a suspicious early
termination on either peer aborts the scan with `ERR_SCAN_INCOMPLETE` and
leaves the index untouched.

## Full-scan checkpoints

A restarted full scan resumes from its checkpoint: rows the interrupted run
committed are identified by `updated_at >= full_scan_started_at` and are
neither redone nor marked missing. The checkpoint is cleared when the scan
completes.

## Upload-state lifecycle

For files above 10 MB the resumable path persists part state in
`upload_progress` keyed `file:<file-row-id>`:

- `file_id` is the foreign key to the pending file row (parsed from the key);
  deleting the file row cascades the state away.
- `telegram_file_id` is Telegram's client-chosen big-file id — it must
  survive process crashes, because resumed parts have to be sent under the
  same id.
- A retry that finds a pending row at the destination with matching identity
  (size, content hash) adopts the row — reusing its id and therefore its
  state key — and sends only unconfirmed parts. Mismatched identity blocks
  with `ERR_PATH_EXISTS`; `--replace` supersedes the pending row and deletes
  its state.
- Hashing is mandatory on the resumable path (`--no-hash` applies only to
  small files), so resume identity never rests on an empty hash.
- Stale state is garbage-collected: `td repair --pending` clears state for
  rows it resolves, supersedes/deletes cascade their state, and a completed
  upload deletes its state.

## Forum transfer and desktop selection

Forum Transfer options additionally persist `logical_root` and `topic_id`;
`channel_tg_id` remains the real peer ID. Retry validates this root still maps
to the recorded peer/topic before claiming work. Switching the current UI or
CLI selection cannot redirect in-flight work or retry. Ordinary options are
unchanged. A removed/rebound root is an explicit error, not a first-peer choice.
Desktop bound-channel DTOs retain `channel_id`; forum entries additionally
provide positive `topic_id` and `selector="root:<absolute-root>"`. Select and
localStorage retain that selector; ordinary entries continue selecting by peer.

## Encrypted archive objects

Client-encrypted archives are ordinary file objects reconstructed through the
existing td:v1 wire metadata and hash of ciphertext. Their private tree and
`.td-archive-v1.json` live only inside age ciphertext. Archive manifest v1
contains ordered unique portable relative entries: `path`, `size`, `hash`
(BLAKE3 for files), `directory`. Manifest size is bounded to 8 MiB. Directory
size/hash is 0/empty. Restore rejects unexpected entries/order/types/sizes,
links, duplicate or escaping paths, invalid hashes and trailing payload,
and consumes age through authenticated EOF. Keys are never stored in SQLite,
remote metadata or config. No database migration is required for encryption.
Randomized ciphertext does not imply plaintext deduplication.
