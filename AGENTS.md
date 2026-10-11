# tg-drive

Go CLI (`td`) and desktop app (`td-gui`) that turn Telegram channels into recoverable, scriptable file trees.

- Run `mise run check` (equivalent to `make ci-local && make build`) before each push.
- Frozen public contracts in `docs/contracts/` take precedence over other prose; source code defines implementation details.
- Work on new scope through GitHub Issues; completed V1 behavior remains the baseline.

Read as needed:
- [Contract changes](docs/agents/contracts.md) — CLI, JSON, storage, config.
- [Go/Telegram invariants](docs/agents/engineering.md) — metadata, safety and formatting.
- [Testing](docs/testing.md) — offline and binary E2E checks.
- [Documentation and ADRs](docs/agents/documentation.md) — current-state docs and design decisions.
- [Architecture](docs/architecture.md), [domain glossary](CONTEXT.md), [CI/release](docs/release-and-ci.md).
