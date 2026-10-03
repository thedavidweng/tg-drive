# Config contract

Default path: `~/.config/tg-drive/config.toml` on Unix, `%APPDATA%\tg-drive\config.toml` on Windows. Data directory: `~/.local/share/tg-drive` on Unix, `%LOCALAPPDATA%\tg-drive` on Windows.

An install that already has `config.toml`, `session.json`, or `gui-session.json` under the previous `tg-drive-cli` config directory keeps using that directory. The same rule applies to `local_cache.db` in the data directory. `TD_CONFIG`, `TD_SESSION`, and `TD_DB` still override these paths. Moving the directories to the `tg-drive` names is optional.

```toml
[telegram]
api_id = 123456
api_hash = "redacted in output"
phone = "+10000000000"

[storage]
db_path = "~/.local/share/tg-drive/local_cache.db"
session_path = "~/.config/tg-drive/session.json"

[caption]
safe_media_caption_utf16_units = 1024
safe_text_message_utf16_units = 4096
margin_utf16_units = 16

[hash]
enabled = true
algorithm = "blake3"

[delete]
mode = "delete"

[limits]
free_upload_bytes = 2147483648
premium_upload_bytes = 4294967296

[upload]
threads = 4
part_size_kb = 0

[transfers]
concurrency = 2

[locks]
ttl_seconds = 900
session_wait_seconds = 30

[rate_limit]
default_wait = false
max_wait_seconds = 300

[[roots]]
local_path = "~/Pictures"
remote_path = "/"
channel_title = "Pictures [TD]"
strategy = "single"
```

Every key above except `[[roots]]` is readable with `td config get <section.key>`
and writable with `td config set`, except `hash.algorithm` (always `blake3`). Integer limits must be positive
(`caption.margin_utf16_units` and `upload.part_size_kb` may be 0).

`transfers.concurrency` bounds how many Transfers one process runs at once;
the rest wait `queued`.

`locks.ttl_seconds` is the lease time of Operation locks and of the Transfer
owner's lease on each Transfer; both renew on a heartbeat at one third of
it, so a Transfer whose owner vanished reads as expired — and is marked
`interrupted` — within about one TTL.

`locks.session_wait_seconds` bounds how long a command that needs Telegram
waits for another process to release the Session lock on the same session
file before failing with `ERR_SESSION_LOCKED`.

The desktop GUI does not use `storage.session_path` itself: its session is
`gui-session.json` beside the resolved CLI session path (see the storage
contract), and it has no config key. Its display preferences — the theme
override (`td-theme`), the language override (`td-locale`), and the Omarchy
switch (`td-omarchy`) — live in the webview's local storage, not here: they
are per-window presentation settings that must apply before the first
paint, before the GUI can call into Go.

Config, session, and database files are kept readable by the current user
only (0600 on POSIX, an owner-only DACL on Windows).

Precedence:

```text
CLI flags > TD_* environment variables > config file > defaults
```
