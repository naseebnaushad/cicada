# AI Onboarding

| | |
|---|---|
| Date | 2026-11-21 |
| Audited | New repository, no commits yet (working tree on `feature/foundation`) |
| Result | Clean baseline; nothing to refactor |

This repository was created with Claude from an empty folder, so there was no existing
code to audit. This file records the baseline so later sessions know where things stand.

## Findings

| Severity | Area | Finding | Proposed fix |
|---|---|---|---|
| Medium | Licence | No licence file; the project is meant to be open source. | Done 2026-11-21: Apache-2.0 `LICENSE` added. |
| Medium | Publishing | Files copied from the closed-source company standards repo (`scripts/version.sh`, `.githooks/`, `.claude/settings.json`, the rules block in `CLAUDE.md`) are in the history. | Decided 2026-11-21: the owner approved publishing them. |
| Low | Versioning | No `VERSION` file, so builds are labelled `0.0.0-dev`. | Open the first version with `/promote`. |
| Low | CI | Actions in `ci.yml` are pinned by version tag, not commit SHA. | Pin by SHA before the first public release. |
| Low | CI | The workflow has never run; the repo has no remote connected. | Connect the remote and open the first PR. |
| Info | Tests | Unit tests cover all code present (CLI, version stamp, S3 spike). | Keep the bar as code arrives. |
| Info | Dependencies | Standard library only. `govulncheck`: no vulnerabilities. | None. |
| Info | Secrets | None in the tree. `.gitignore` covers `.env*`, keys and certificates. | None. |

## Architecture verdict

Too early to judge. The target structure (clean architecture, ports and adapters) is
defined in section 3 of the [Action Plan](Action%20Plan.md) and the code so far does not
conflict with it.

## Decision log

| Date | Decision | By |
|---|---|---|
| 2026-11-21 | Go; runner layer over GitHub Actions; action plan approved; Milestone 0 started. | Owner |
| 2026-11-21 | No clean-up proposed: nothing to clean. | Claude |
| 2026-11-21 | Licence is Apache-2.0. Repo stays private until the licence is committed and the company-file question is settled. | Owner |
| 2026-11-21 | The files copied from the company standards repo may be published; the repo goes public. | Owner |
