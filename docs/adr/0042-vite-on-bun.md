# 0042: Run Vite on Bun

Status: Accepted. Supersedes the Vite runtime decision in ADR 0035.

Context: ADR 0035 made Bun the frontend's package manager and test runner
but kept Vite on Node LTS because Bun as a Vite runtime was unproven. That
left the GUI build needing two JavaScript runtimes. Vite's `bin/vite.js`
has a `#!/usr/bin/env node` shebang, so `bun run build` with a plain
`vite build` script still ran Vite on Node.

An evaluation on macOS (arm64) with the pinned Bun 1.4.2, Node 24.21.0,
and Vite 8.3.2 compared `bun run build` (Vite on Node) against
`bunx --bun vite build` (Vite on Bun), and the same for `--mode demo`:

- Both runtimes built both modes successfully (2052 modules).
- The output was byte-identical: `diff -r` of `dist/` and `dist-demo/`
  from the two runtimes showed no differences, including the asset hashes
  (`index-CG1V6oZe.js`, `index-DE5I7yKs.css`).
- Wall time over three runs (the first run cold): app build on Node
  1.38 / 0.44 / 0.34 s, on Bun 0.74 / 0.29 / 0.30 s; demo build on Node
  0.40 / 0.36 / 0.33 s, on Bun 0.32 / 0.29 / 0.29 s. Vite itself reported
  409 ms on Node and 167 ms on Bun for the cold app build.
- `bun --bun vite` started the dev server (ready in 329 ms). The listening
  process was the Bun binary; `/`, `/src/main.tsx`, and `/src/index.css`
  returned 200, and editing a source file produced an HMR update.

Decision:

- The frontend's `dev`, `build`, and `build:demo` scripts run
  `bun --bun vite ...`, so Vite runs on Bun everywhere those scripts are
  used: the Makefile `gui-*` targets, CI, release packaging, the site
  workflow, and the UI preview harness.
- The GUI CI job and the release GUI packaging jobs install only Go and
  Bun through mise.
- Node stays pinned in `mise.toml` because `site/publish.mjs` and the UI
  preview harness (Playwright through npm, plus small `node -e` helpers in
  `ui-preview/run.sh`) still need it. The site and UI preview workflows
  keep installing it.

Considered options:

- Keep Vite on Node. Rejected: the output is identical, Bun is faster,
  and the GUI build no longer needs a second runtime.
- Drop Node entirely. Rejected: Playwright and the site publisher run on
  Node; moving them is separate work with its own risk.

Consequences: Building the GUI needs only Go and Bun. A Bun upgrade now
also changes the Vite runtime, so a Bun upgrade must pass the frontend
build as well as the tests. The evaluation ran on macOS; the Linux and
Windows GUI CI jobs are the gate for the other platforms. If a Vite or
plugin release breaks on Bun, reverting is a script change plus restoring
`node` in the GUI CI and release `install_args`.
