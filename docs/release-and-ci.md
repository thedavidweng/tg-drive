# Release and CI

## Tools

- GitHub Actions for CI/CD
- release-please for version and changelog PRs
- GoReleaser v2 for release assets
- nfpm through GoReleaser for deb/rpm/apk packages
- cosign keyless signing for checksums
- syft SBOM generation
- Homebrew cask published to `thedavidweng/homebrew-tap`

For the td-gui artifacts (all driven by the pinned `wails3` CLI, see
"Desktop GUI"):

- nfpm through `wails3 tool package` for the Linux .deb
- linuxdeploy with the GTK plugin through `wails3 generate appimage` for the
  AppImage (downloads linuxdeploy and AppRun from their upstream
  "continuous" releases at package time)
- hdiutil through `wails3 tool package -format dmg` for the macOS .dmg
- NSIS (`makensis`) for the Windows installer

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
  own Sync Releases workflow updates `Casks/tg-drive.rb` from the
  published release assets (daily cron or manual dispatch).

`RELEASE_PLEASE_TOKEN` is an optional override. `CODECOV_TOKEN` is only
needed for private repos.

## Workflows

- `.github/workflows/ci.yml` runs three jobs:
  - `test` (25m): tidy, fmt, vet, lint, unit tests, race, `make build`, coverage
  - `gui` (45m, matrix of ubuntu/macos/windows): `make check-gui`,
    `make gui-build`, and a packaging dry run — `make gui-package-<os>` with
    the placeholder version 0.0.0, uploaded as a workflow artifact. This is
    what verifies the release packaging scripts on pull requests (see
    "Desktop GUI" below).
  - `snapshot` (30m): `goreleaser build --snapshot --clean` on its own runner
- `.github/workflows/release-please.yml` manages release PRs, tags, and
  releases, then dispatches packaging.
- `.github/workflows/release.yml` runs on `v*` tags or when dispatched with
  a tag name:
  - `release`: GoReleaser builds and publishes the CLI artifacts (CGO-free;
    unchanged by the GUI jobs).
  - `gui-linux` (ubuntu-latest and ubuntu-24.04-arm), `gui-darwin`
    (macos-latest), `gui-windows` (windows-latest): build td-gui natively
    and upload the packages to the same release. All three `need` the
    `release` job so the GitHub Release exists before they upload.
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
7. The `gui-linux`, `gui-darwin`, and `gui-windows` jobs build td-gui
   natively per OS and upload the dmg, NSIS installer, AppImage, and deb
   (plus `.sha256` sidecars) to the same release.
8. The tap repository's Sync Releases workflow updates the Homebrew
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
on Windows it installs `make` and `nsis` via Chocolatey (the runners ship
Git Bash but no make). Tools come from `mise-action` at the pinned versions.
The job is informational, not a required check.

### GUI packaging

`make gui-package-<linux|darwin|windows>` builds td-gui and wraps it in the
platform package, writing versioned artifacts and `.sha256` sidecars to
`dist/gui/`. The targets depend on `gui-frontend-build` and `gui-tools`, and
each is a thin wrapper over a script under `build/<os>/package.sh`, so what
CI runs is exactly what runs locally:

```sh
make gui-package-linux GUI_VERSION=1.2.3    # .deb + AppImage (host arch)
make gui-package-darwin GUI_VERSION=1.2.3   # universal .dmg
make gui-package-windows GUI_VERSION=1.2.3  # NSIS installer (x64)
```

`GUI_VERSION` is the release version without the leading `v`; it defaults to
`0.0.0` for local and pull-request dry runs. The scripts use the pinned
`wails3` CLI for the heavy lifting — nfpm for the .deb, linuxdeploy with its
GTK plugin for the AppImage, hdiutil for the DMG, plus icon (.ico/.icns),
.syso, and WebView2-bootstrapper generation — while the packaging inputs
(desktop file, nfpm config, Info.plist, NSIS script) are plain files under
`build/`, not the Wails Taskfile layout (which assumes `main.go` beside
`frontend/`).

The macOS bundle is ad-hoc signed (`codesign --sign -` — arm64 Mach-O
requires a signature to run) and everything is otherwise unsigned: Windows
SmartScreen and macOS Gatekeeper warnings on first launch are expected, and
the README documents how to open the app anyway. The NSIS installer is
per-user (no UAC prompt) and installs the WebView2 runtime when the system
lacks it.

The CI `gui` job runs the matching target on every pull request as a dry run
and uploads `dist/gui/` as a workflow artifact, so packaging breakage shows
up before a tag does. The AppImage step downloads linuxdeploy from its
unversioned "continuous" release; that is the one unpinned input in the
pipeline (upstream has no stable tags to pin).

Windows is packaged for x64 only and macOS as a single universal binary;
Linux is packaged natively on both x64 and arm64 runners.

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
adding a scene is a new entry plus any seed data in `run.sh`. A scene's
`spawn` names an environment variable `run.sh` exports holding a shell
command to start once the scene's page loaded — how the Transfers scenes
run a CLI upload against the same fake Telegram. The GUI's native file
dialogs are no-ops in server mode, so the build answers them from
`TD_GUI_PICK_FILES` / `TD_GUI_PICK_DIR` (see `cmd/td-gui/picker.go`);
`run.sh` points those at staged files. Scenes run
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

The td-gui artifacts (unsigned; each with a `.sha256` sidecar), for tag
`v1.2.3`:

```text
td-gui_1.2.3_darwin_universal.dmg
td-gui_1.2.3_windows_x86_64-installer.exe
td-gui_1.2.3_linux_x86_64.AppImage
td-gui_1.2.3_linux_aarch64.AppImage
td-gui_1.2.3_linux_amd64.deb
td-gui_1.2.3_linux_arm64.deb
```

The arch names follow each ecosystem's convention: `amd64`/`arm64` for deb,
`x86_64`/`aarch64` for AppImage.
