# Contract changes

Interfaces in `docs/contracts/` are frozen unless explicitly changed with corresponding documentation and verification. On a contract change:
- Commands and flags -> update `docs/contracts/cli-contract.md`.
- JSON output -> update `docs/contracts/json-contract.md`.
- Storage or manifest semantics -> update `docs/contracts/storage-contract.md`.
- Config keys/defaults -> update `docs/contracts/config-contract.md`.

Every command must preserve stable `--json` output. JSON uses stdout; logs, prompts, progress and diagnostics use stderr. Public exit-code changes are contract changes. Relevant new dependencies, abstractions, or changes to public JSON/exit-code contracts require an ADR.

Use `DECISIONS.md` for currently accepted product constraints; source code for implementation detail. Where prose disagrees, documented contracts take precedence over other docs (not over explicit changes approved through the design process).
