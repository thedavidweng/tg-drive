# CLI contract

Binary: `td`

## Global flags

```text
--config <path>
--db <path>
--session <path>
--json
--quiet
--verbose
--channel <name-or-id>
--root <local-root>
--wait
--no-wait
```

`--channel` selects a bound drive. An exact Telegram ID takes precedence over
a title with the same text. A title shared by multiple bound drives is rejected
with `ERR_USAGE` (exit 2); select an explicit Telegram ID from `td status` for
an ordinary channel, or a unique `--root` when multiple topics share a peer.
An unmatched selector returns `ERR_CHANNEL_NOT_FOUND`.

Initial `init --bind-channel=<title-or-id>` selection applies the same identity
rule to remote Telegram dialogs: an exact ID wins, and a title must be unique
across the complete dialog lookup, including the optional ` [TD]` title alias.
Ambiguous titles return `ERR_USAGE` (exit 2); use an ID from `td channels list`.
A failed or interrupted lookup leaves the binding unchanged. When an init
caller supplies a title to resolve or create, creation occurs only after
`ERR_CHANNEL_NOT_FOUND` (exit 4), never after ambiguity, authentication,
rate-limit or transport errors.

`--root` selects a drive by its persisted local-root binding, independent of
Telegram titles. Relative paths are made absolute against the current directory;
symlinks are not resolved. It overrides `TD_CHANNEL`; combining it with an
explicit nonempty `--channel` returns `ERR_FLAG_CONFLICT` (exit 2). An unknown
root returns `ERR_CHANNEL_NOT_FOUND` (exit 4), including for `status`; multiple
bindings at the same root return `ERR_USAGE` (exit 2) before writes. `init` still
binds and scans its own positional local root. With neither selector nor
`TD_CHANNEL`, the first bound drive remains the default.

## Commands

```text
td version
td auth setup
td auth login [--resend]
td auth status
td auth logout
td channels list [--only-drive]
td channels link-discussion
td init <local-root>
  [--create-channel [=<title>]]
  [--bind-channel [=<title-or-id>]]
  [--topic <positive-existing-topic-id>]
  (bare --bind-channel lists and prompts)
  # ordinary channels ensure a linked discussion group on demand
  # --topic requires --bind-channel, refuses --create-channel and binds an
  # existing open/visible forum topic; records are same-topic replies
  # select forum drives with --root, not their ambiguous shared peer ID
td status
  # succeeds before init (data.initialized=false); lists bound channels
  # never waits for another process's Session lock: while one holds it,
  # the account line reads unknown and the rest comes from the index
td doctor
  td doctor path-codec
td scan [remote-root]
  [--full] [--strict] [--repair] [--include-deleted] [--events]
  # --events streams NDJSON scan.stage / scan.item events, then the final
  # scan line (see the JSON contract)
td ls [remote-path]
td dedupe [remote-path]
  [--delete --confirm]
  # read-only by default; --delete retains the first path in each duplicate
  # group and retires the remaining Telegram messages
td tree [remote-path]
  [--depth <n>]
td watch <local-directory> <remote-directory> --confirm
  [--delete] [--once] [--interval <duration>]
  # one-way regular-file content sync; default interval 2s
  # requires hash.enabled=true; rejects symlinks, special files and local
  # names that change under remote canonical path normalization
  # --delete retires only checkpointed paths absent locally
  # otherwise local removals retain remote content; empty dirs are ignored
  # remote content conflicts stop the watch; restart is explicit
td completion [bash|zsh|fish|powershell]
td cp <local> [local...] <remote-path>
  [--replace] [--skip-existing] [--auto-rename] [--no-hash]
  [-r|--recursive] [--continue-on-error] [--include-empty-dirs]
  [--upload-threads <n>] [--upload-part-size-kb <n>]
  [--confirm] [--dry-run] [--events]
  # typed uploads (single-file and multi-file; rejected with --recursive)
  [--as <photo|video|document>]        # presentation kind (default document)
  [--duration <seconds>]               # video length (--as video)
  [--width <px>] [--height <px>]       # video dimensions (--as video)
  [--streaming]                        # streaming hint (--as video)
  [--thumb <file.jpg>]                 # JPEG thumbnail (document/video kinds)
```

Two argument forms:

- `td cp <local> <remote-path>` — single file, one message per upload; the
  caption contains only the caller's human text and display name. The remote
  parent path and `#td_*` path tags are not rendered into new captions. The
  machine record is a `td-manifest:v1` comment on the message's discussion
  thread (ADR 0018)
- `td cp <local...> <remote-dir>` — two or more sources publish as native
  Telegram media groups (albums): one human-only caption on the first
  member, empty sibling captions, one `td-album:v1` inventory comment on
  the first member's discussion thread per group (ADR 0018). The caption
  contains no generated parent path or `#td_*` path tags. Sets larger than
  10 members split into consecutive groups of up to 10.
  `<remote-dir>` is `/`, a path ending in `/`, or an existing remote
  directory. `--replace` is not supported in this form (`ERR_USAGE`);
  `--skip-existing` and `--auto-rename` apply per file, and a lone survivor
  after skips publishes as an ordinary single message with its own
  inventory comment.

`--recursive` uploads each source directory's direct children as one album
(split at 10); nested directories recurse.

Every `td cp` and `td get` form runs as a Transfer (ADR 0033): the Transfer
is recorded in the index, visible to `td transfers` in any process while it
runs and after it ends, and the command waits for it in the foreground.
Result output is unchanged. The Transfer kinds are `upload` (single-file
cp), `download` (single-file get), `album_upload` (multi-file cp, one
Transfer for the whole album call), `recursive_upload`, and
`recursive_download`. `td cp --events` emits one `transfer.stage` event
per stage the Transfer enters and one `transfer.progress` event per
confirmed upload part, then the final `cp` line. Ctrl-C cancels the command's Transfers: they end
`cancelled`.

`--as photo` sends a native photo message: Telegram recompresses the bytes,
downloads fetch the largest representation, and strict size/hash verification
does not apply to them. `--as video` keeps the bytes untouched. In multi-file
form the presentation flags apply uniformly to every member. See
`docs/integration-notes.md` for the full semantics of the three content
forms.

```text
td get <remote-path> <local-dest>
  [-r|--recursive] [--replace] [--skip-existing] [--auto-rename] [--continue-on-error]
  [--events]
  # --events streams one transfer.stage event per stage the Transfer enters,
  # then the final get line (see the JSON contract)
td transfers list
  [--active | --all] [--stage <stage>]
  # reads the index only: never waits for another process's Session lock
  # --active (the default) lists Transfers that have not ended; --all
  # includes ended ones; --stage lists only that stage
td transfers show <id>
td transfers cancel <id>
  # reads and writes the index only: never waits for another process's
  # Session lock. Sets the Transfer's cancel-requested flag; the owning
  # process notices on its lease heartbeat and ends the Transfer
  # `cancelled`, a queued one without starting it. Cancelling an ended
  # Transfer fails with ERR_USAGE; an unknown ID with
  # ERR_TRANSFER_NOT_FOUND.
td transfers watch
  [--events]
  # follows Transfers live until Ctrl-C (exit 130, ERR_CANCELLED); reads
  # the index only: it sees every process's Transfers and never waits for
  # a Session lock. It rereads the index every 250 ms, so a Transfer that
  # passes a stage between two polls is reported at its later stage
  # --events (or --json): streams NDJSON transfer.stage events (see the
  # JSON contract)
  # on a terminal: redraws a live table of the active Transfers and the
  # ones that ended since the watch started
  # piped: prints one line per stage a Transfer enters, in the
  # `transfers list` format
td transfers retry <id>
  [--events]
  # re-runs a failed, cancelled, or interrupted Transfer from its recorded
  # request (source, destination, and options), in the foreground like the
  # cp/get form it came from: the command waits and exits with the outcome.
  # The retrying process becomes the Transfer's owner; the Transfer keeps
  # its ID and created_at. An interrupted upload resumes from the parts
  # upload_progress saved; a download starts over. Retrying a running or
  # completed Transfer fails with ERR_USAGE, an unknown ID with
  # ERR_TRANSFER_NOT_FOUND. Unlike the other transfers subcommands, retry
  # connects to Telegram, so it can wait for the Session lock.
  # --events streams one transfer.stage event per stage the retried
  # Transfer enters, transfer.progress lines for upload kinds as td cp
  # emits them, then a final transfers.retry line with the ended Transfer.
td mv <remote-from> <remote-to>
  [--confirm] [--dry-run]
  # moves one file or every indexed file below a directory. Directory moves
  # preserve source-relative paths; when the destination is an existing
  # directory, the source directory name is appended. All destinations are
  # validated before Telegram mutation. A remote failure may leave a partial
  # move; error details include operation_id, from, resolved to, moved,
  # failed, and remaining. Retry the original source and requested destination
  # to move remaining indexed files without changing the resolved destination.
  # after indexed files finish, move remaining local folders transactionally,
  # including an entirely empty subtree; counts remain indexed-file counts.
  # moving into / retains the source basename like any existing directory.
td rm <remote-path>
  [--tombstone] [--allow-stale-manifest]
  [--confirm] [--dry-run]
  # a machine record left stale (manifest not redacted, album inventory
  # not rewritten) fails with ERR_TELEGRAM_RPC after the file is removed;
  # --allow-stale-manifest exits 0 with stale_manifest: true instead;
  # directory results also include operation_id for durable progress;
  # after all indexed files are removed, delete empty local directory nodes
  # in that subtree too, including folders created by the GUI
td share [remote-path]
td adopt [message-id] [remote-path]
  [--unmanaged] [--into <dir>]
  [--hash]
  [--rewrite-captions]
  # --rewrite-captions converts a legacy channel to the ADR 0018 comment
  # model: strips machine lines from captions (one human caption per
  # album), deletes per-file td-manifest replies, and posts one
  # td-album:v1 inventory comment per group
  # --hash downloads each adopted file to compute and store its BLAKE3
  # content hash, so post-rebuild downloads verify content
td import saved [message-id...]
  [--into <dir>] [--photos-as <document|photo>]
  [--merge-captions] [--delete-source] [--no-dedupe]
  [--replace|--skip-existing|--auto-rename]
  [--dry-run] [--confirm] [--continue-on-error] [--events]
  # re-upload Saved Messages content into the drive channel; forwarded
  # provenance is recorded as td-origin:v1 and skipped captions as
  # td-dupe:v1 comments in the linked discussion group
  # --photos-as is required for photo messages in --json/non-interactive runs
  # --confirm is required unless --dry-run; --delete-source also requires it
  # old in-place claim forms fail with ERR_USAGE naming td adopt
td repair [path]
td repair --pending
td repair --orphaned [--delete-orphaned --confirm]
td repair --scan-errors
td repair [--hash] [path]
  # --hash downloads every active file under [path] that lacks a content
  # hash, computes its BLAKE3 digest, and backfills it into the index and
  # the machine record on Telegram (comment thread, legacy reply, or
  # caption carrier). A zero/missing size is repaired from the download.
td repair --captions [path]
  [--dry-run] [--continue-on-error]
  # removes td's former parent-path and #td_* caption scaffold from modern
  # comment-carrier rows; exact path/hash/MIME/tags remain in discussion
  # manifests. Legacy td:v1 caption carriers are skipped.
td config get [key]
  [--show-secrets] [--confirm]
  # --show-secrets without --confirm prompts on a terminal and fails with
  # ERR_CONFIRMATION_REQUIRED in --json mode or without a terminal
td config set <key> <value>
```

`--verbose` (or `TD_VERBOSE=1`) writes `debug:` diagnostics to stderr:
resolved paths, connection setup, per-RPC latency, retries, and flood waits.
It never prints credentials, phone numbers, or file contents, and does not
change stdout.

`--confirm` is required for `td rm`, `td mv`, `td cp --replace`, `td adopt`,
`td import saved` (unless `--dry-run`), and `td repair --delete-orphaned`.

SIGINT (Ctrl-C) and SIGTERM cancel the running command. It stops at the
next item or transfer part, releases its operation locks, ends its
Transfers `cancelled`, and exits 130
with `ERR_CANCELLED` (an error envelope on stdout under `--json`). An
interrupted large upload keeps its confirmed parts, so running the same
`td cp` again resumes it. A prompt waiting for input is cancelled too. An
interruption that leaves state needing repair still reports
`ERR_ORPHANED_UPLOAD` or `ERR_REPAIR_REQUIRED`, so the remedy is not lost. A
second signal terminates the process at once.

JSON envelopes include `meta` as specified in `docs/contracts/json-contract.md`. The short envelopes below omit `meta` for brevity.

## Global JSON success envelope

```json
{
  "ok": true,
  "data": {}
}
```

## Global JSON error envelope

```json
{
  "ok": false,
  "error": {
    "code": "ERR_CODE",
    "message": "human readable message",
    "details": {}
  }
}
```

## Error codes

```text
ERR_USAGE
ERR_FLAG_CONFLICT
ERR_AUTH_REQUIRED
ERR_AUTH_FAILED
ERR_CONFIG_MISSING
ERR_CONFIG_INVALID
ERR_CHANNEL_NOT_FOUND
ERR_CHANNEL_PERMISSION
ERR_PATH_INVALID
ERR_PATH_EXISTS
ERR_PATH_IS_DIRECTORY
ERR_PATH_ANCESTOR_IS_FILE
ERR_PATH_CONFLICT
ERR_LOCAL_PATH_EXISTS
ERR_LOCAL_NOT_FOUND
ERR_REMOTE_NOT_FOUND
ERR_TRANSFER_NOT_FOUND
ERR_FILE_TOO_LARGE
ERR_CAPTION_TOO_LONG
ERR_MANIFEST_INVALID
ERR_ALBUM_INVENTORY_INVALID
ERR_SCAN_INCOMPLETE
ERR_CROSS_CHANNEL_MOVE
ERR_EMPTY_DIRS_UNSUPPORTED
ERR_MESSAGE_NOT_EDITABLE
ERR_SCAN_FAILED
ERR_TELEGRAM_RATE_LIMITED
ERR_TELEGRAM_RPC
ERR_CONFIRMATION_REQUIRED
ERR_DB
ERR_OPERATION_LOCKED
ERR_SESSION_LOCKED
ERR_ORPHANED_UPLOAD
ERR_REPAIR_REQUIRED
ERR_SLUG_COLLISION
ERR_CANCELLED
ERR_UNKNOWN
```

## Exit code mapping

| Exit | Meaning | Error codes |
|---:|---|---|
| 0 | Success | none |
| 1 | General error | uncategorized errors |
| 2 | Usage/input error | `ERR_USAGE`, `ERR_FLAG_CONFLICT`, `ERR_PATH_INVALID`, `ERR_PATH_EXISTS`, `ERR_PATH_IS_DIRECTORY`, `ERR_PATH_ANCESTOR_IS_FILE`, `ERR_PATH_CONFLICT`, `ERR_LOCAL_PATH_EXISTS`, `ERR_LOCAL_NOT_FOUND`, `ERR_REMOTE_NOT_FOUND`, `ERR_TRANSFER_NOT_FOUND`, `ERR_CROSS_CHANNEL_MOVE`, `ERR_DIRECTORY_MOVE_UNSUPPORTED`, `ERR_DIRECTORY_DELETE_UNSUPPORTED`, `ERR_EMPTY_DIRS_UNSUPPORTED`, `ERR_SLUG_COLLISION` |
| 3 | Auth/config error | `ERR_AUTH_REQUIRED`, `ERR_AUTH_FAILED`, `ERR_CONFIG_MISSING`, `ERR_CONFIG_INVALID` |
| 4 | Telegram/platform error | `ERR_CHANNEL_NOT_FOUND`, `ERR_CHANNEL_PERMISSION`, `ERR_FILE_TOO_LARGE`, `ERR_MESSAGE_NOT_EDITABLE`, `ERR_TELEGRAM_RATE_LIMITED`, `ERR_TELEGRAM_RPC` |
| 5 | DB/index/repair error | `ERR_DB`, `ERR_SCAN_FAILED`, `ERR_MANIFEST_INVALID`, `ERR_ALBUM_INVENTORY_INVALID`, `ERR_SCAN_INCOMPLETE`, `ERR_OPERATION_LOCKED`, `ERR_SESSION_LOCKED`, `ERR_ORPHANED_UPLOAD`, `ERR_REPAIR_REQUIRED`, `ERR_CAPTION_TOO_LONG` |
| 10 | Confirmation/safety error | `ERR_CONFIRMATION_REQUIRED` |
| 130 | Cancelled by SIGINT or SIGTERM | `ERR_CANCELLED` |

### Existing forum topics

`td init <root> --bind-channel=<exact-peer-id> --topic <positive-id>` binds
one existing topic. It verifies forum/topic identity, open/visible state,
required mutation capabilities and history access. No forum/topic is created.
The explicit topic peer is not overridden by `TD_CHANNEL`. Ordinary init
behavior and envelopes remain unchanged. `--topic=0`, missing --bind-channel,
or a create/topic combination returns `ERR_USAGE` (exit 2); permission refusal
uses `ERR_CHANNEL_PERMISSION` (exit 4). An unscoped legacy forum binding must
be resolved explicitly. `td channels link-discussion` rejects forum roots.

Use `td --root <root> ...` for upload, album, download, scan, move, delete,
preview and dependent mutations. Same-topic replies carry the existing manifest
and album records; no new remote metadata version is introduced. General ID 1
follows Telegram's unflagged-message semantics. `td doctor` reports topic state;
closed/hidden/missing topics are not writable. These are source contracts, not
receipts for authenticated Telegram, a new release or clean-device signing.

## Read-only filesystem adapters

`td webdav [remote-directory] --listen 127.0.0.1:8080` serves the selected
subtree over loopback HTTP. A literal loopback IP is required; port 0 chooses
a free port. GET/HEAD (including byte ranges), PROPFIND and OPTIONS are allowed;
mutation/lock methods return 405. Non-loopback Host headers return 403; browser
content is sandboxed with no active resource loading. This endpoint has no
authentication and is intended for local clients only.

`td mount <remote-directory> <local-mountpoint>` exposes the same read-only
subtree through installed native FUSE on Linux/macOS. The mountpoint must be an
existing empty directory, not a symlink. No driver installation or privilege
escalation occurs. Other systems return ERR_USAGE. Mount/driver errors fail
explicitly. SIGINT/SIGTERM cancels reads and requests normal unmount.

Both adapters pin account/peer/topic/logical-root identity at startup and refuse
rebinding. Changing the active selector cannot retarget them. Only seekable
Telegram representations are supported. There is no writable mount or WebDAV
mutation support.

## Client-encrypted archives

```text
td archive keygen <identity-file>
td archive put <local-directory> <remote-file> --recipient <age1...>
td archive restore <remote-file> <new-local-directory> --identity <private-key-file>
```

Keygen exclusively creates an owner-private X25519 identity file, prints only
the public recipient, and refuses an existing identity (ERR_LOCAL_PATH_EXISTS).
Put encrypts file names, directory entries, hashes and bytes inside an age
recipient-encrypted tar stream before the existing upload protocol sees them.
The explicitly selected remote object name, ciphertext size and public age
recipient information are not hidden from Telegram. The remote object is an
ordinary immutable file and cannot replace another path through this command.

Restore requires the user's matching owner-private regular identity file. It
downloads the encrypted object, decrypts and verifies its entire versioned
manifest/entry stream, including the final authenticated chunk, in private
staging. Publication uses native exclusive rename in the destination parent;
even an existing empty destination is refused (ERR_LOCAL_PATH_EXISTS). Invalid
ciphertext, unsupported archive version, unsafe paths or mismatched hashes fail
with ERR_MANIFEST_INVALID and never publish a destination. A cancelled operation
retains ERR_CANCELLED/130. There is no fallback copying or overwrite mode.

Manifest version 1 is inside the encrypted object; it is distinct from the
existing Telegram td:v1 reconstruction metadata. Index-loss full scan can
recover the encrypted object but cannot recover its separately owned private
key. Losing that key loses access. age recipient encryption provides
confidentiality and ciphertext integrity, not sender authentication. No key
escrow or automatic key recovery exists. Restored mode is private 0700/0600;
ownership, ACLs, executable bits and timestamps are not preserved. Only portable
regular files/directories are supported; links and special files are refused.
