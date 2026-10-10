# Contributing

Thanks for wanting to improve `td`. Please open or comment on a
[GitHub Issue](https://github.com/thedavidweng/tg-drive/issues) before
starting large work.

## Setup

```sh
git clone https://github.com/thedavidweng/tg-drive.git
cd tg-drive
make bootstrap
make ci-local
```

You need Go 1.26 or newer. `make lint` also needs
[golangci-lint](https://golangci-lint.run/) v2.

## Tests

```sh
make test
make test-race
make ci-local          # fmt-check, vet, tests, race
```

To exercise the CLI without a Telegram account or network, use the in-memory
fake (login code is `12345`):

```sh
export TD_FAKE_TELEGRAM=1 TD_FAKE_TELEGRAM_STATE=/tmp/td-demo/fake.json
export TD_API_ID=1 TD_API_HASH=hash TD_PHONE=+1000
td auth login
td init ./testdata/local --create-channel
td cp ./testdata/local/a.txt /a.txt
td ls /
```

Real-account checks are documented in
[`docs/manual-smoke-tests.md`](docs/manual-smoke-tests.md). Use a private
test channel, not a production one.

## Contracts

Command names, JSON envelopes, storage schema, and config keys are frozen in
[`docs/contracts/`](docs/contracts/). Update the matching contract in the
same change:

| Change | Contract |
| --- | --- |
| Command or flag | `docs/contracts/cli-contract.md` |
| JSON envelope or error | `docs/contracts/json-contract.md` |
| Schema or manifest | `docs/contracts/storage-contract.md` |
| Config key or default | `docs/contracts/config-contract.md` |

New dependencies, new abstractions, or public JSON/exit-code changes also
need an ADR in `docs/adr/NNNN-slug.md`.

## Style

- Format with `gofumpt` (strict extra-rules).
- JSON goes to stdout. Logs, prompts, and diagnostics go to stderr.
- Remote writes use an operation lock and a DB transaction.
- Do not assume Telegram capabilities. Use the capability layer and
  `td doctor`.
- Count Telegram captions in UTF-16 code units, not bytes or runes.
- Machine recovery uses `td:v1`, `td-manifest:v1`, or `td-album:v1`. Hashtags
  are navigation only.

## Commits and pull requests

Use [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat: add scan rebuild
fix: handle UTF-16 caption budget
docs: update storage contract
test: add slug collision cases
ci: update release workflow
```

Before opening a PR:

- [ ] `mise run check` passes
- [ ] Contracts updated if the public surface changed

## License and CLA

The project is distributed under [AGPL-3.0-only](LICENSE). Before an external
contribution is merged, its contributor must sign [CLA version 1.0](https://github.com/thedavidweng/tg-drive/blob/cb4785014fbf8f5e41fc3392993885e86219f144/CLA.md).
The bot links the agreement and asks you to post this comment in the PR:

> I have read the CLA Document and I hereby sign the CLA

Use your own GitHub account. No external login, OAuth authorization, or
maintainer approval comment is required. A recorded signature is reused for
future contributions to this project under the same agreement version.
Comment `recheck` to refresh a check after correcting contributor identity.

Contributors retain copyright. The CLA grants David Weng rights to distribute
accepted contributions under other open-source, commercial, and proprietary
terms, including closed-source paid or mobile editions. Users of an AGPL
release do not need to sign a CLA.

See [LICENSING.md](LICENSING.md) for historical grants and third-party licenses.

### Maintainer setup

Publish `cla-signatures` before publishing this workflow. This separate,
unprotected branch contains the versioned agreement and the signature JSON;
records start empty. Require the **`CLA` commit status** from GitHub Actions
(app ID 15368) in the default branch's protection after the workflow is live.
Do not require `CLA Assistant`: comment-triggered runs belong to the default
branch; the `CLA` status explicitly targets the checked PR commit.

Each changed agreement version needs a new pinned document URL and a new
signature-file path. Do not treat existing version 1 signatures as consent to
a changed agreement. Historical contributions are not automatically signed.

The workflow never checks out or executes PR code. The upstream action is
pinned to Vapourfly's version 2.6.1, whose repository is now archived. It checks
at most 100 commits, reads the first page of PR comments, and does not parse
coauthor trailers. An external PR opener must also be a GitHub-linked commit
author; mismatched identity fails the check. Split larger PRs, sign before the
thread grows long, and
verify any additional coauthors' acceptance during the normal rights review.
A bot exemption is not evidence of ownership; third-party and employer rights
still need to be respected.
