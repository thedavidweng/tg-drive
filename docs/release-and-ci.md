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
