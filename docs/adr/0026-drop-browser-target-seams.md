# 0026 - Drop browser-target seams and WASM gate

## Status

Accepted. Supersedes the browser-target parts of ADR 0009.

## Context

`td` carried abstractions that existed only for a browser/WASM host:
`core/drive.Runtime` (bundling `Store`, `Files`, and `Telegram`), the
`ports.Store`, `ports.Telegram`, and `ports.Clock` interfaces,
`adapters/browser/memorystore`, `adapters/fakefs`, the `cmd/td-wasm` stub,
and CI steps that compiled `core/...` and the stub for `GOOS=js GOARCH=wasm`.
Only `Runtime.Files` was ever read; the service used `App.DB` and `App.TG`
directly, and no code or test imported the in-memory adapters. A browser
target is not planned.

## Decision

- Delete `core/drive`, `ports.Store`, `ports.Telegram`, `ports.Clock`,
  `adapters/browser/memorystore`, `adapters/fakefs`, and `cmd/td-wasm`.
- `service.App` no longer has a `Runtime` field; the service uses
  `adapters/native/localfs` through `ports.FileSystem`.
- Remove the `test-core-wasm` and `build-wasm` Make targets and their CI steps.
- Keep the core import guard (`core/core_test.go`) as a layering rule: core
  imports no adapters, `internal/*`, storage drivers, gotd/td, or cobra. The
  WASM-only bans on `os` and `path/filepath` are dropped.
- Keep `ports.FileIndex` and `ports.FileSystem`, which have real consumers.

## Consequences

- Fewer types to read and keep in sync; no hypothetical seam needs to
  reappear elsewhere.
- Core packages may use `os` and `path/filepath`, so `core/...` is no longer
  guaranteed to compile for `js/wasm`.
- Reintroducing a browser target would need a new ADR and its own adapters.
