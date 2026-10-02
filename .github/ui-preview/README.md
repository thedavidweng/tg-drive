# UI preview scenes

Scripted Playwright scenes that drive the real `td-gui` in Wails' headless
server mode against the fake Telegram — no secrets, no network. The
workflow that runs these on every GUI PR is issue #68; this directory
holds the scenes and their contract.

## Scene contract

A scene is a Node script in `scenes/`:

```
node scenes/<name>.js <base-url> <out-dir>
```

It drives the app at `<base-url>` with Playwright and writes PNG
screenshots to `<out-dir>`, exiting non-zero on failure. Scenes capture at
2x (e.g. a 1920x1280 viewport) so the published previews are crisp.

## Server contract

The harness builds `td-gui` with `-tags gui,server` and starts it with:

- `HOME=<sandbox>` — config and the GUI session resolve under `$HOME`, so a
  fresh sandbox starts on the setup screen; pre-write
  `.config/tg-drive-cli/config.toml` to start configured.
- `TD_FAKE_TELEGRAM=1` and `TD_FAKE_TELEGRAM_STATE=<path>` — the persistent
  fake Telegram (docs/testing.md).
- Optional fake knobs: `TD_FAKE_AUTH_PASSWORD=<pw>` (the account has 2FA),
  `TD_FAKE_LOGIN_FLOOD_WAIT=<seconds>` (login is rate-limited).
- `WAILS_SERVER_PORT=<port>` — the scene's `<base-url>`.

## Scenes

- `login.js` — first-run setup, login with the code prompt, a failed
  login, the logged-in shell, and logout (#69).
