# tg-drive Domain Glossary

## Core concepts

- **td** — The CLI binary name. Short for "Telegram Drive".
- **Root** — A binding between a local directory and a Telegram channel, stored in the SQLite cache and `config.toml`.
- **Channel** — A Telegram channel used as the remote storage target.
- **Virtual file tree** — The path hierarchy presented by `td ls`, `td tree`, `td cp`, `td get`, and related commands. It is reconstructed from Telegram media and `td:v1` / `td-manifest:v1` / `td-album:v1` records, then cached in SQLite.
- **Canonical path** — The stable, slash-separated path used in the SQLite index and manifest (e.g. `/Pictures/2024/beach.jpg`).
- **Display name** — The human-readable file or directory name stored in a caption or manifest.
- **Manifest** — Reconstructable machine metadata on Telegram. Since ADR 0018 it lives as a comment in the file's discussion thread: one `td-manifest:v1` comment per ungrouped file, one `td-album:v1` comment per album group. Legacy in-channel replies and `td:v1` captions remain parseable.
- **File record** — The machine record of one file: its per-file `td-manifest:v1` comment (or legacy reply / `td:v1` caption), or its entry in the `td-album:v1` inventory its album shares. `td rm`, `td mv`, `td repair <path>`, and `cp --replace` change a file through its File record without branching on which kind it is (ADR 0028).
- **Caption** — The human text attached to a Telegram media message: caller-provided text and display name. Modern captions do not include td's remote parent path or path-derived hashtags; the machine record is the discussion-thread comment.
- **Discussion group** — The supergroup linked to a drive channel. Every channel post auto-forwards there, and its comment thread carries the post's machine record (ADR 0018).
- **Hashtag** — A `#` prefixed human token in a caption, or a path-derived `#td_*` token retained in index/manifest data for compatibility. Path-derived tags are not emitted in modern captions and are not the reconstructable storage model.
- **Node** — A file or directory entry in the SQLite index (`nodes` table).
- **File row** — A row in the `files` table tracking message ID, manifest ID, hash, status, and local path.
- **Status** — The lifecycle of a file row: `pending`, `active`, `deleted`, `superseded`, `missing`, `invalid`, `orphaned`.
- **Content hash** — A `blake3` (or configured) hash of the file bytes, used for integrity checks.
- **Slug** — A collision-resistant hashtag segment (`readable_prefix` + 8-character BLAKE3 suffix) used in `#td_` chains. Not the canonical path.
- **Resumable upload** — A big-file upload whose part state is persisted in `upload_progress` so it can continue after interruption.
- **Operation lock** — A row in `operation_locks` that serializes path-touching operations across processes.
- **Operation** — One use case's exclusive claim on canonical paths of the bound channel: it holds their Operation locks while its body runs, and every Telegram write happens inside one (ADR 0030).
- **Transfer** — One user-requested upload or download, tracked from the moment it is queued until it ends, under a stable Transfer ID. A Transfer is not an Operation: its execution runs inside one Operation, but it also exists before the locks are taken (queued) and after they are released (completed, failed, cancelled).
- **Transfer stage** — Where a Transfer is in its lifecycle: `queued`, `hashing`, `uploading` / `downloading` (with byte progress), `publishing`, then one terminal stage of `completed`, `failed`, `cancelled`, or `interrupted`. `interrupted` means the Transfer owner disappeared mid-run; retrying it resumes from saved upload parts.
- **Transfer owner** — The one process running a Transfer, holding a lease on it that it renews on the Operation-lock heartbeat (the `locks.ttl_seconds` TTL). Other processes see the Transfer and may request its cancellation through the index, but only the owner acts on it. A Transfer whose lease expires — its owner vanished mid-run — is marked `interrupted` by the next process to read it. Retrying an `interrupted` Transfer makes the retrying process its new owner.
- **Transfer Manager** — The module that queues Transfers, runs them with bounded concurrency, reports their stages, and cancels or retries them. It is shared by every front end; it is not part of any one of them.
- **Observer** — What one long-running service call reports to while it runs: each item's stage changes, byte progress (done and total), and per-item result (completed, skipped, failed). It belongs to the call, not to the App, so concurrent calls never share one. The Transfer Manager turns these reports into Transfer stages; `td cp --events` turns them into `cp.progress` lines.
- **Front end** — A program a person uses to drive td: the `td` CLI or the `td-gui` desktop app. Front ends share the local index and see each other's Transfers through it; they do not talk to each other directly.
- **Session lock** — The exclusive claim one process holds on a Telegram login session while it is connected, so two processes never use the same login at once. Each front end has its own session.
- **Omarchy mode** — The GUI's flat look adopted automatically on the Omarchy desktop: the current Omarchy theme's colours and Hyprland's window rounding and border replace the design tokens, and a Settings switch turns it off. Not a theme: when it is on, the light/dark override steps aside.
- **File publisher** — The module that publishes a file to the index, turning a canonical path, display name, content hash, and Telegram message into a file row, manifest, hashtag tags, slug mappings, and derived nodes. It renders the caption, tags, and slug maps of a publication in one pass, and indexes a media group all-or-nothing.
- **Adopt** — Claim an existing message of the bound drive channel into the virtual file tree without re-uploading bytes (`td adopt`). The media message stays where it is; td writes machine records and indexes the file.
- **Import** — Re-upload content from an external Telegram chat into the bound
  drive channel (`td import saved`). Importing Saved Messages copies fresh
  bytes, mirrors Saved Messages sub-chats below `/saved`, and preserves
  source provenance.
- **Saved Messages** — Telegram's self chat. Forwarded media stored there can
  disappear when the origin channel deletes its post, so `td import saved`
  makes a new drive-channel upload. Saved Messages 2.0 sub-chats are mirrored
  as directories.
- **Origin record** — An additive `td-origin:v1` discussion comment naming
  where an imported file or album came from. It is provenance, not the
  authoritative path record.
- **Duplicate record** — An additive `td-dupe:v1` discussion comment naming
  an existing file whose bytes matched a skipped Saved Messages item and
  preserving that item's caption.
- **Scan** — Reading a Telegram channel's messages and rebuilding the local SQLite index.
- **Tombstone** — A soft delete: the Telegram message is edited to a tombstone caption instead of being removed.

## Sources of truth

- Product constraints: `DECISIONS.md`
- CLI surface: `docs/contracts/cli-contract.md`
- JSON envelope: `docs/contracts/json-contract.md`
- Storage schema: `docs/contracts/storage-contract.md`
- Config keys: `docs/contracts/config-contract.md`
- ADRs: `docs/adr/`
