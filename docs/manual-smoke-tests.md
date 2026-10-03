# Manual smoke tests

Use a private Telegram test channel. Do not use personal production channels.

## Required environment

Create a Telegram app at https://my.telegram.org/apps, then either:

```sh
td auth setup
```

or:

```sh
export TD_API_ID=...
export TD_API_HASH=...
export TD_PHONE=...
```

`td auth setup` also creates the session and database directories.

## Prerequisites

- Build the binary: `make build`.
- The upload fixture `testdata/local/a.txt` is in the repo. Downloads land in
  `testdata/restore/`, which `td get` creates on demand (gitignored).

## Login

- You only need to log in once. The session persists to
  `~/.config/tg-drive/session.json`. `td auth status --json` reports
  whether a login is necessary.
- Re-running `td auth login` while a code is pending reuses that code. Pass
  `--resend` only when you need a fresh one.
- Do not spam code requests. Telegram flood-waits accounts after a few
  `SendCode` calls (observed blocks last about 24 hours). The CLI prints the
  wait duration and resume time; retrying earlier does not help.
- A mistyped code re-prompts without sending a new one. An expired code
  triggers one automatic resend.
- If the code was accepted but the 2FA password failed, re-running
  `td auth login` resumes at the password prompt.

## Sequence

`td init <local-root>` binds the **local root directory** for the channel
index (`root_local_path`). It is not an upload source. Uploads are always
explicit `td cp <local> <remote>` calls.

```sh
td auth setup
td auth login
td auth status --json
td init ./testdata/local --create-channel
td doctor --json
td cp ./testdata/local/a.txt /a.txt --json
td ls / --json
td tree / --json
td get /a.txt ./testdata/restore/a.txt --json
td mv --confirm /a.txt /renamed.txt --json
td rm --confirm /renamed.txt --json
td scan --full --json
td share / --json
```

## Old edit capability

Keep a test message for at least several days, then run:

```sh
td doctor --json
```

Doctor must report whether old media caption edit is supported, unsupported,
or unknown for the current account and channel.

## Desktop app (td-gui)

The UI preview (`ui-preview/run.sh`) covers every screen in server mode,
which has no tray, no window hooks, no single-instance lock, and no native
dialogs. Check those on each desktop OS against a release package
(`make gui-package-<os> GUI_VERSION=<version>`), logged in to a test
channel. Record the OS version, the package version, and pass/fail per item.

Release stamp:

- `td-gui --version` prints the packaged version, not `dev`.
- Settings → About shows the same td-gui version, and the installed `td`
  version (or "Not installed" when no `td` is on PATH, in `TD_INSTALL_DIR`,
  or in the installer's default directory).

Window and tray:

- The titlebar fits the platform: on macOS the traffic lights sit inset in
  the header; on Windows the system title bar takes the page's light or
  dark colours and follows a theme switch. Dragging the header moves the
  window.
- Start a large upload, then close the window. The window hides, the tray
  icon stays, and the tray menu lists the running Transfer with live
  progress. The Transfer keeps running (`td transfers list --json` from a
  terminal shows its bytes growing).
- Click the tray icon or its Transfer row: the window comes back, focused,
  on the same screen. On macOS, clicking the dock icon does the same.
- Launch td-gui a second time: no second window opens; the running window
  comes forward.

Quit:

- With no Transfer running, Quit from the tray (and Cmd+Q on macOS) exits
  at once.
- With a Transfer running, Quit asks first. "Keep Running" leaves it
  running; "Quit" exits, and the Transfer shows as interrupted in
  `td transfers list --all --json`, retryable with `td transfers retry <id>`
  or from the GUI's Transfers tab after a relaunch.

Native dialogs and drop:

- Upload files / Upload folder / the download destination / the thumbnail
  "Choose…" open the system file dialogs, and a cancelled dialog changes
  nothing.
- Dropping files from the system file manager onto the Drive view opens the
  upload sheet with them.

Live settings and channels:

- Change `transfers.concurrency` in Settings while uploads are queued: the
  number running at once follows the new value without a restart.
- With the window open, run `td init <root> --create-channel=Other` in a
  terminal: the drive switcher lists "Other" within a few seconds.
- Switch drives while an upload runs: it finishes, and the Transfers tab
  keeps showing it.
