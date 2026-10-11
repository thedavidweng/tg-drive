# Go and Telegram invariants

- Run `gofumpt` with strict extra rules (through `mise run check`).
- For every remote write, obtain the operation lock and use a DB transaction.
- Check Telegram capabilities through the capability layer and `td doctor`; do not infer support.
- Count caption length as UTF-16 code units, never `len(string)` or number of runes.
- Reconstruct structured content from `td:v1`, `td-manifest:v1` and `td-album:v1` metadata; hashtags are for human browsing, not machine reconstruction.
- For GUI-related tasks, follow the existing Bun/Vite frontend and typed-binding build pipeline in `mise.toml`, rather than treating the frontend as the Go CLI.
