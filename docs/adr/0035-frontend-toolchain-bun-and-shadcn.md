# 0035: Frontend toolchain: Bun for packages and tests, Node for Vite, shadcn/ui on magpie tokens

Status: Accepted.

Context: ADR 0031 fixes the GUI stack (Wails v3, React, TypeScript, Vite,
Tailwind CSS, shadcn/ui) and the magpie design, but leaves the JavaScript
tooling open: which package manager installs the frontend, which runner
executes the behaviour tests, and how shadcn/ui's neutral defaults meet
magpie's tokens. Wails v3 is beta and the spec requires exact pins
everywhere, so every choice must be pinnable and CI-reproducible.

Decision:

- Bun is the frontend's package manager, pinned in `mise.toml`, with
  `bun.lock` committed and CI installing with `bun install
  --frozen-lockfile`. All dependency versions in `frontend/package.json`
  are exact (no ranges), matching the Wails pinning rule.
- Vite runs under Node LTS, also pinned in `mise.toml`. Bun as the Vite
  runtime stays unproven and is revisited separately.
- Frontend behaviour tests run on Bun's built-in test runner with
  happy-dom and `@testing-library/react`, not Vitest: Bun is already
  installed for package management, so the test runner adds no second
  runtime, and the tests only need DOM rendering, which happy-dom covers.
- TypeScript stays on the 6.x line: typescript-eslint 8 supports
  `typescript <6.1`, and `@typescript/native-preview` (7.x) is not adopted
  while the lint toolchain lags.
- shadcn/ui components are vendored source under
  `frontend/src/components/ui` (the shadcn model), styled by mapping the
  shadcn/Tailwind theme variables onto the magpie tokens in
  `frontend/src/index.css` rather than editing generated components.
- The `wails3` CLI is pinned in the Makefile (`WAILS3_VERSION`) at the exact
  Wails module version in `go.mod` and installed by `make gui-tools` with a
  leading-`v` `go install` query; mise's Go backend drops the `v`, and that
  query fails on Ubuntu CI. `make gui-bindings` regenerates the committed
  TypeScript bindings and `make gui-bindings-check` fails CI on drift.

Considered options:

- npm/pnpm with Vitest. Rejected: two tools where Bun covers both, and
  slower installs for no capability the tests use.
- Importing magpie's stylesheet wholesale. Rejected: it styles magpie's
  DOM, not shadcn components; only the token values are copied.
- Generating bindings in CI without committing them. Rejected: a committed,
  drift-checked artifact keeps `go vet -tags gui` and the frontend
  type-check hermetic and reviewable.

Consequences: GUI work needs `mise install` once; the default gates never
touch Node, Bun, or the frontend. Upgrading Bun, Node, TypeScript, or Wails
is an explicit PR that updates the pins together.
