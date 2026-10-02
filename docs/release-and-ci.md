# Release and CI

## Tools

- GitHub Actions for CI/CD
- release-please for version and changelog PRs
- GoReleaser v2 for release assets
- nfpm through GoReleaser for deb/rpm/apk packages
- cosign keyless signing for checksums
- syft SBOM generation
- Homebrew cask published to `thedavidweng/homebrew-tap`

## Repository secrets

None are required. The pipeline runs end to end on the built-in
`GITHUB_TOKEN`:

- release-please creates the release PR and, after the merge, the tag and
  the GitHub Release.
- The Release Please workflow then dispatches the Release workflow via
  `workflow_dispatch` (the one event type the built-in token is allowed
  to trigger), because tags created with the built-in token do not fire
  the tag trigger.
- The Homebrew cask is not pushed from this repo. The tap repository's
  own Sync Releases workflow updates `Casks/tg-drive-cli.rb` from the
  published release assets (daily cron or manual dispatch).

`RELEASE_PLEASE_TOKEN` is an optional override. `CODECOV_TOKEN` is only
needed for private repos.

## Workflows

- `.github/workflows/ci.yml` runs three jobs:
  - `test` (25m): tidy, fmt, vet, lint, unit tests, race, `make build`, coverage
  - `gui` (30m, matrix of ubuntu/macos/windows): `make check-gui` and
    `make gui-build` (see "Desktop GUI" below)
  - `snapshot` (30m): `goreleaser build --snapshot --clean` on its own runner
- `.github/workflows/release-please.yml` manages release PRs, tags, and
  releases, then dispatches packaging.
- `.github/workflows/release.yml` runs GoReleaser on `v*` tags or when
  dispatched with a tag name.
- `.github/workflows/ui-preview.yml` records screenshots and a video of
  the real GUI on UI pull requests (see "UI preview" below).

## Release process

1. Merge conventional commits into `main`.
2. release-please opens or updates a release PR.
3. Merge the release PR.
4. release-please creates a tag and the GitHub Release.
5. The Release Please workflow dispatches the Release workflow on the
   tag (`workflow_dispatch`), because tags created with the built-in
   token do not fire the tag trigger.
6. GoReleaser uploads archives, packages, checksums, signatures, and
   SBOMs to the release.
7. The tap repository's Sync Releases workflow updates the Homebrew
   cask from the published assets (daily cron; may lag by up to a day,
   or run it manually after a release).

## Desktop GUI

`td-gui` (ADR 0031) is built with the `gui` tag and is not part of the
default gates: it links the platform webview through cgo (GTK 4 and
WebKitGTK 6.0 on Linux), while `mise run check` stays CGO-free and needs no
webview.

The GUI toolchain is pinned in `mise.toml`: Node LTS (runs Vite) and Bun
(frontend package manager and test runner). The `wails3` CLI is pinned in
the Makefile (`WAILS3_VERSION`) at the exact Wails module version in
`go.mod` and installed into `dist/bin` by `make gui-tools`, which every
target that needs it depends on. It is not a mise tool: mise's Go backend
drops the leading `v` when it invokes `go install`, and that version query
fails on Ubuntu CI.

```sh
mise run check-gui     # or: make check-gui
```

`check-gui` runs, in order:

- `make gui-bindings-check`: regenerates the TypeScript bindings
  (`wails3 generate bindings -f '-tags gui' -ts -i -d frontend/bindings
  ./cmd/td-gui`; the layout has `main.go` away from `frontend/`, so Wails'
  default Taskfiles do not apply) and fails on any drift — the bindings are
  committed.
- `make gui-frontend-check`: `bun install --frozen-lockfile`, then the
  frontend type-check (`tsc -b`), lint (`eslint --max-warnings 0`), and
  behaviour tests (`bun test`).
- `make gui-go-check`: builds the frontend into `frontend/dist` (the
  `gui`-tagged `frontend` package embeds it, so the Go side cannot compile
  without it), then `go vet -tags gui ./...` and `go test -tags gui` over
  the GUI packages.

`make gui-build` additionally produces `dist/td-gui`.

The CI `gui` job runs the same targets natively on Ubuntu, macOS, and
Windows. On Ubuntu it first installs `libgtk-4-dev` and `libwebkitgtk-6.0-dev`;
on Windows it installs `make` via Chocolatey (the runners ship Git Bash but
no make). Tools come from `mise-action` at the pinned versions. The job is
informational, not a required check.

## UI preview

Every pull request that touches `frontend/`, `internal/gui/`,
`cmd/td-gui/`, or the pipeline itself gets screenshots and a screen
recording of the real app in a marked block
(`<!-- td-gui-preview:start -->` … `:end -->`) at the foot of its
description; re-runs replace the block in place. The harness lives in
`ui-preview/` and works identically on a developer machine:

```sh
ui-preview/run.sh out     # seeds, builds, serves, records into out/
```

`run.sh` seeds a drive through the real CLI against the fake Telegram
(`TD_FAKE_TELEGRAM_STATE`, login code `12345`), builds td-gui with
`-tags gui,server` — Wails' headless server mode, CGO-free and needing no
webview — and drives it with Playwright (`record.mjs`, pinned in
`ui-preview/package.json`). It serves two instances: the seeded,
authenticated one the Drive scenes record, and a credential-free one for
the setup and login scenes (its fake account has two-step verification,
`TD_FAKE_AUTH_PASSWORD`). Each scene is one entry in the scenes list in
`record.mjs` (color scheme, locale, optional theme override or
interaction, and `setup` to record against the credential-free server);
adding a scene is a new entry plus any seed data in `run.sh`. Scenes run
in order against shared server state, so a scene that starts something
the next one depends on (or must not see) uses `leave` to restore a clean
state after its shot. The run writes one 2x PNG per scene, `preview.mp4`,
and a `manifest.json` the publisher consumes.

The workflow has two jobs with a strict security split (ADR 0036):

- `record` builds and runs the PR's code, so it holds
  `permissions: contents: read` and checks out the PR with no persisted
  credentials. It uploads the recording as an artifact.
- `publish` holds `contents: write` and `pull-requests: write` but never
  checks out or runs the PR's code: it downloads the artifact, checks out
  only the default branch for `ui-preview/publish.mjs`, pushes the media
  to the `previews` branch (one directory per open PR, force-with-lease,
  closed PRs dropped), and rewrites the PR description block. Fork pull
  requests hold a read-only token regardless, so `publish` skips them.

One-time setup: enable GitHub Pages on the repository (Settings → Pages →
deploy from the `previews` branch, root). The Actions token cannot enable
Pages, so an admin does this once. Until then the block still works: the
screenshots link `raw.githubusercontent.com` and only the video player
page needs Pages. The workflow is informational — never add it to the
required branch-protection checks.

## Local checks

```sh
make goreleaser-check
make snapshot
```

## Asset names

Archives follow the `archives.name_template` in `.goreleaser.yaml`
(<span v-pre><code>{{ .ProjectName }}_{{ .Os }}_&lt;arch&gt;</code></span>, where amd64 renders as `x86_64` and
the macOS universal binary renders as `universal`):

```text
td_darwin_universal.tar.gz
td_linux_x86_64.tar.gz
td_linux_arm64.tar.gz
td_windows_x86_64.zip
td_windows_arm64.zip
```

Packages use GoReleaser's default nfpm name template. For tag `v1.2.3`:

```text
td_1.2.3_linux_amd64.deb
td_1.2.3_linux_arm64.deb
td_1.2.3_linux_amd64.rpm
td_1.2.3_linux_arm64.rpm
td_1.2.3_linux_amd64.apk
td_1.2.3_linux_arm64.apk
```

Signing, checksums, and SBOMs:

```text
checksums.txt
checksums.txt.sig
*.spdx.json
```
