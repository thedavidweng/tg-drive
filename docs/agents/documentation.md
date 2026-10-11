# Documentation, delivery and ADRs

- Keep repository reference docs current-state only; do not create archived plans or speculative "Phase N later" paragraphs.
- Track future work in `thedavidweng/tg-drive` GitHub Issues (`gh issue create`), not future-state reference docs. PRs are not a separate triage queue.
- Keep `docs/architecture.md`, `DECISIONS.md` and `CONTEXT.md` scoped to their purposes; do not restate them in agent docs.
- Record decisions about new dependencies, abstractions or changes to public JSON/exit-code contracts as sequential `docs/adr/NNNN-slug.md` records with Status / Context / Decision / Consequences. ADRs capture **why**, not implementation recipes.
- Review relevant contract documentation and `mise run check` before opening/finishing a PR.
