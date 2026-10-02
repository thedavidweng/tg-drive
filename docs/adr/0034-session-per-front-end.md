# 0034: One Telegram session per front end, guarded by a Session lock

Status: Accepted.

Context: Telegram may answer `AUTH_KEY_DUPLICATED` and invalidate the
login when one authorization key holds parallel main-DC connections from
separate processes. gotd's `FileSessionStorage` takes only an in-process
mutex and rewrites the session file in place, without a temp file and
rename. It also rewrites the file on every new MTProto session. Two `td`
processes on one session file can therefore lose the login or corrupt the
file today, and a long-lived GUI would make that much more likely.

Decision:

- The GUI uses its own session file and logs in once on its own. It
  appears as a separate device in Telegram.
- A process takes an exclusive Session lock, an OS file lock beside the
  session file, when it first connects to Telegram, and releases it when
  it disconnects. Commands that read only the local index never connect,
  so they never wait for it.
- A second process that needs a held session waits for a bounded time,
  then fails with a dedicated error code.
- Session files are written atomically: temp file, fsync, rename.

Consequences: The CLI and the GUI run side by side. Two CLI processes
that both need Telegram on the same session run one after the other
instead of risking the login. The JSON contract gains one error code.
