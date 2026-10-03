# 0037: Rename the project to tg-drive

Status: Accepted.

Context: The repository, Go module, and default config directory were named
`tg-drive-cli`. The desktop app `td-gui` ships from the same repository and
calls the same service layer (ADR 0031), so the name described one front end.
The binary names `td` and `td-gui` already matched the product.

Decision: The GitHub repository and module path are
`github.com/thedavidweng/tg-drive`. New installs use `tg-drive` for the
config directory, the data directory, the Homebrew cask, and the Windows
install directory. An existing install keeps its `tg-drive-cli` config
directory when that directory already holds `config.toml`, `session.json`,
or `gui-session.json`, and keeps its data directory when `local_cache.db`
is already there. `TD_CONFIG`, `TD_SESSION`, and `TD_DB` still win.

Considered options:

- Two repositories, one for the CLI and one for the GUI. Rejected: both
  front ends share the index, the Transfer manager, and one release.
- Keep the repository name and only retitle the README. Rejected: the
  module path and on-disk directory would still say the product is a CLI.

Consequences: `go install` and raw install-script URLs use the new
repository. GitHub redirects the old repository URL. Downstream release
clients (the Stash plugin, the Homebrew cask) use the new repository and
cask token. `brew install --cask tg-drive-cli` follows `cask_renames.json`
to `tg-drive`.
