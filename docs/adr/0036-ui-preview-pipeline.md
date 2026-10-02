# 0036: UI preview pipeline on scripted scenes over Wails server mode

Status: Accepted.

Context: Pull requests that change the td-gui frontend or the GUI facade
(ADR 0031) are hard to review from a diff alone; reviewers need to see the
real app. magpie solves this with a UI preview pipeline whose scenes are
planned by an LLM, which needs an API secret in CI. This repo keeps no
secrets (docs/release-and-ci.md), and the fake Telegram client
(`TD_FAKE_TELEGRAM_STATE`) already drives complete workflows offline, so
scenes can be scripted instead of planned. The pipeline needs a way to run
the real facade without a display, and a safe way to publish results: a
job that runs arbitrary PR code must never hold a write token.

Decision: Record previews with Playwright against a server-mode td-gui
build, scripted scenes, no secrets; publish from a separate job that never
runs PR code.

- Wails' `server` build tag (`go build -tags gui,server`) serves the
  embedded frontend, the generated bindings, and the events WebSocket over
  HTTP (`WAILS_SERVER_HOST`/`WAILS_SERVER_PORT`), and it excludes the
  platform webview, so the build is CGO-free and headless. Verified:
  Playwright drives the real facade over HTTP with
  `TD_FAKE_TELEGRAM{,_STATE}` seeding the drive through the CLI, the same
  flow the binary E2E tests use.
- Scenes are a data-driven list in `ui-preview/record.mjs` (theme, locale,
  and optional interaction per entry). Each scene renders at 2x device
  scale; the run also captures one walkthrough video, normalised to mp4.
- The harness (`ui-preview/run.sh`) is the same locally and in CI, so a
  preview can be reproduced on a developer machine.
- The workflow runs the PR's code only in a `record` job with
  `permissions: contents: read` and no credentials in the checkout. A
  separate `publish` job downloads the artifacts, pushes them to the
  `previews` branch served by GitHub Pages, and rewrites a marked block in
  the PR description; it checks out only the default branch.
- Playwright is pinned in `ui-preview/package.json` (npm, with a committed
  lockfile), matching the Node version pinned in `mise.toml`. It is
  CI tooling only; nothing ships.

Considered options:

- An LLM-planned scene list, as magpie does. Rejected: it needs a secret
  in CI and produces non-repeatable runs; scripted scenes against the fake
  Telegram are deterministic and need no secrets.
- Driving the desktop build under a virtual display (Xvfb). Rejected:
  server mode needs no display, no webview dependencies, and no cgo, and
  the facade it exercises is the same one the desktop app binds.
- `pull_request_target` so fork PRs get previews too, as magpie does.
  Rejected for now: plain `pull_request` never grants a write token to a
  run that started from fork code; fork PRs simply skip publishing.
- Commenting the preview on the PR instead of editing its description.
  Rejected: the description block stays current in place and never spams
  notifications.

Consequences: Screenshots and recordings are public once pushed to the
`previews` branch, which Pages serves; the branch accumulates one directory
per open PR and drops closed ones on each push. Adding a scene is an entry
in `record.mjs` plus any seed data in `run.sh`. The preview job is
informational and must stay out of required branch-protection checks.
Enabling Pages on the `previews` branch is a one-time manual step
(docs/release-and-ci.md); until then the block still links the raw files.
