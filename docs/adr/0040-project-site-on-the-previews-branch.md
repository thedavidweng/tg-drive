# 0040: Project site on the previews branch

Status: Accepted. The no-build-tool point is amended by ADR 0041 (live demo).

Context: The project needs a public website at
`thedavidweng.github.io/tg-drive/`. A repository has one GitHub Pages site,
and it already serves the `previews` branch, where the UI preview
(ADR 0036) publishes one `pr-*/` directory per open pull request. The site
is a single landing page; it needs no framework, and new frontend tooling
would be a dependency to maintain for one page.

Decision: The site is static files in `site/`, published to the root of
the `previews` branch next to the preview directories.

- `site/publish.mjs publish` (run by `.github/workflows/site.yml` on pushes
  to `main`) replaces everything at the branch root except `pr-*/`, as one
  orphan commit pushed with `--force-with-lease`, retrying on a race the
  same way the preview publisher does.
- `ui-preview/publish.mjs` keeps an existing root `index.html` and writes
  its placeholder only when the root has none.
- The site reuses `assets/icon.png` and `assets/screenshot.png` instead of
  copies; the publisher copies them into `img/`.
- No build tool or new dependency: HTML, CSS, and a small script.

Considered options:

- Pages from GitHub Actions artifacts (`actions/deploy-pages`). Rejected:
  it replaces the branch source, so the previews would need to move into
  the same artifact build.
- A separate `thedavidweng.github.io` repository path. Rejected: the site
  belongs with the code and the README screenshot it shows.
- A framework-built site (TanStack Start, as in the design reference).
  Rejected for one page: a build chain and dependencies for no feature the
  static page lacks.

Consequences: Both publishers write the same branch and must keep each
other's files: the site owns the root, the preview owns `pr-*/`. A change
to either publisher's pruning has to respect that split. Editing the site
is editing `site/`; the deploy follows the push to `main`.
