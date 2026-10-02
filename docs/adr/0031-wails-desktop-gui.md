# 0031: Wails v3 desktop GUI outside the service layer

Status: Accepted.

Context: td needs a desktop GUI with full CLI parity. The CLI is pure Go
and cross-compiles with `CGO_ENABLED=0`. Desktop webview toolkits need CGO
and native webview libraries (WebKitGTK on Linux), so a GUI that leaks into
shared packages would break the CLI build and its gates. The look and feel
follows yetone/magpie, which also uses Wails v3.

Decision: Adopt Wails v3 for the desktop GUI and keep the GUI framework
outside the application and service layers.

- `cmd/td-gui` is the only main package that imports Wails.
  `internal/gui` is a thin facade: one Wails service each for Drive, Auth,
  Transfers, and Settings, plus typed events registered in one place. It
  translates between the frontend and `internal/service` /
  `internal/transfer`, and holds no business rules.
- `internal/service`, `internal/transfer`, `core/*`, and `adapters/*`
  never import Wails or `internal/gui`. The CLI and the GUI call the same
  use cases.
- Every GUI Go file carries the `gui` build tag. Default `go build`,
  `go vet`, and `go test` over `./...` never see Wails, so the CLI and the
  default gates stay CGO-free. The GUI has its own build and CI job with
  `-tags gui`.
- The frontend is React and TypeScript built with Vite, styled with
  Tailwind and shadcn/ui, and managed with Bun. It lives in `frontend/` at
  the repo root. A tagged Go package inside it embeds the built assets,
  because `go:embed` cannot reach parent directories.
- The frontend calls Go only through Wails-generated TypeScript bindings
  and typed events. It does not use an HTTP API.
- Linux uses Wails' default GTK4 and WebKitGTK 6.0 stack.
- Dependencies track the latest releases, including Wails v3 betas.

Considered options:

- A Wails-free localhost HTTP API, as magpie does. Rejected: generated
  bindings give typed calls and events without a second hand-written
  contract.
- A `nogui` opt-out tag, as magpie does. Rejected: the CLI is the primary
  product, so CGO must be opt-in, not opt-out.
- A nested Go module for the GUI. Rejected: it needs replace directives
  and splits dependency upgrades for little isolation gain over a build
  tag.

Consequences: `go.mod` lists Wails even though the CLI binary never links
it. GUI release artifacts need native CGO builds on each OS. Confirmation
gates and other rules the GUI needs must live in `internal/service`, not in
the Cobra commands (ADR 0032).
